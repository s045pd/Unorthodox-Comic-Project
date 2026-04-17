import asyncio
import base64
import random
import threading
from logging import getLogger
from typing import List, Tuple

import aiohttp
from asgiref.sync import sync_to_async
from celery import shared_task
from celery_once import QueueOnce
from django.core.exceptions import ObjectDoesNotExist
from django.core.files.base import ContentFile
from django.db import transaction
from django.db.models import Exists, OuterRef, Q

from apps.models import Book, Episode, Image, Tag
from apps.services import ImageExtractor, http_client
from apps.tools import images_to_long_image, long_image_to_pdf
from SE8 import celery_app

logger = getLogger("celery")

# ============== 并发控制参数 ==============
MAX_CONCURRENT_REQUESTS = 20  # 最大并发请求数
BATCH_SIZE = 20  # 每批处理的任务数


# ============== 事件循环管理 ==============
_loop_local = threading.local()


def get_event_loop():
    """获取当前线程的事件循环，不存在则创建（复用）"""
    if not hasattr(_loop_local, "loop") or _loop_local.loop.is_closed():
        _loop_local.loop = asyncio.new_event_loop()
        asyncio.set_event_loop(_loop_local.loop)
    return _loop_local.loop


def run_async(coro):
    """在复用的事件循环中执行协程"""
    loop = get_event_loop()
    return loop.run_until_complete(coro)


# ============== 并发下载工具 ==============


async def download_image_with_semaphore(
    semaphore: asyncio.Semaphore,
    session: aiohttp.ClientSession,
    image_id: str,
    url: str,
) -> Tuple[str, bytes]:
    """带信号量控制的图片下载"""
    async with semaphore:
        try:
            async with session.get(
                url, headers={"referer": "https://se8.us/"}
            ) as response:
                if response.status == 200:
                    content_type = response.headers.get("Content-Type", "")
                    if content_type.startswith("image"):
                        data = await response.read()
                        return (image_id, data)
        except Exception as e:
            logger.error(f"Error downloading {url}: {e}")
        return (image_id, b"")


async def download_batch_concurrent(
    batch: List[Tuple[str, str]],
) -> List[Tuple[str, bytes]]:
    """并发下载一批图片，使用全局 HTTP 客户端"""
    semaphore = asyncio.Semaphore(MAX_CONCURRENT_REQUESTS)
    session = await http_client.get_session()

    tasks = [
        download_image_with_semaphore(semaphore, session, image_id, url)
        for image_id, url in batch
    ]
    results = await asyncio.gather(*tasks, return_exceptions=True)

    # 过滤掉异常和空结果
    return [(id_, data) for id_, data in results if isinstance(data, bytes) and data]


async def save_images_batch(images_data: List[Tuple[str, bytes]]):
    """批量保存图片到数据库"""
    if not images_data:
        return

    # 获取所有需要更新的图片对象
    image_ids = [id_ for id_, _ in images_data]
    image_map = {id_: data for id_, data in images_data}

    images_to_update = await sync_to_async(
        lambda: list(Image.objects.filter(id__in=image_ids).only("id", "image"))
    )()

    for img_obj in images_to_update:
        if img_obj.id in image_map:
            img_obj.image = base64.b64encode(image_map[img_obj.id]).decode()

    # 批量更新
    await sync_to_async(
        lambda: Image.objects.bulk_update(images_to_update, ["image"], batch_size=100)
    )()


# ============== 书籍发现任务 ==============


async def process_books():
    """处理书籍发现，收集后批量调度"""
    books_to_process = []

    async for data in ImageExtractor().get_books():
        current_episode = data.pop("current", None)
        book, created = await sync_to_async(Book.objects.update_or_create)(
            id=data.pop("id"),
            defaults=data,
        )
        if created or await sync_to_async(book.is_outdated)(
            episodes_title=current_episode
        ):
            logger.info(f"Find book: {book.title}")
            books_to_process.append(book.id)

    # 批量调度，错开执行时间避免任务风暴
    for i, book_id in enumerate(books_to_process):
        find_episodes.apply_async(
            args=[book_id],
            countdown=5 + (i * 2),  # 每个任务间隔 2 秒
        )


@celery_app.task(
    base=QueueOnce,
    once={"graceful": True, "timeout": 30 * 60},
    soft_time_limit=25 * 60,
    time_limit=30 * 60,
)
def find_books():
    """
    Find books from the website and create or update Book objects
    Usage: from apps.tasks import find_books as t;t();
    """
    run_async(process_books())


# ============== 章节发现任务 ==============


async def process_episodes(book_id: str):
    """处理章节发现"""
    try:
        book = await sync_to_async(Book.objects.get)(id=book_id)
    except ObjectDoesNotExist:
        logger.error(f"Book with id {book_id} does not exist.")
        return

    episodes_to_process = []

    async for data in ImageExtractor().get_episodes(book.raw_url):
        if "tags" in data:
            for tag in data["tags"]:
                tag_obj, _ = await sync_to_async(Tag.objects.get_or_create)(name=tag)
                await sync_to_async(book.tags.add)(tag_obj)
            book.hot = data["hot"]
            book.description = data["description"]
            await sync_to_async(book.save)(update_fields=["hot", "description"])
        else:
            episode, created = await sync_to_async(Episode.objects.update_or_create)(
                id=data.pop("id"),
                book=book,
                defaults=data,
            )
            if created:
                logger.info(f"Find episode: {episode.title}")
                episodes_to_process.append(episode.id)

    # 批量调度图片下载任务
    for i, episode_id in enumerate(episodes_to_process):
        find_images.apply_async(
            args=[episode_id],
            countdown=5 + (i * 3),  # 每个任务间隔 3 秒
        )


@shared_task(
    bind=True,
    autoretry_for=(Exception,),
    retry_backoff=True,
    retry_backoff_max=600,
    retry_jitter=True,
    max_retries=3,
    rate_limit="10/m",
)
def find_episodes(self, book_id: str, start_index: int | None = None):
    """
    Find episodes for a specific book and create or update Episode objects
    Usage: from apps.tasks import find_episodes as t;t(Book.objects.first().id);
    """
    run_async(process_episodes(book_id))


# ============== 图片下载任务 ==============


async def process_images(episode_id: str, force: bool = False):
    """处理图片发现和下载"""
    try:
        episode = await sync_to_async(Episode.objects.get)(pk=episode_id)
    except ObjectDoesNotExist:
        logger.error(f"Episode {episode_id} not found")
        return

    images_to_download = []
    extractor = ImageExtractor()

    async for data in extractor.get_images(episode.raw_url):
        image, created = await sync_to_async(Image.objects.update_or_create)(
            id=data.pop("id"),
            episode=episode,
            defaults=data,
        )
        if created or force:
            images_to_download.append((str(image.id), image.raw_url))
            logger.info(f"Find image: {episode.title} - {image.index}")

    if not images_to_download:
        return

    # 分批并发下载
    for i in range(0, len(images_to_download), BATCH_SIZE):
        batch = images_to_download[i : i + BATCH_SIZE]
        downloaded = await download_batch_concurrent(batch)
        await save_images_batch(downloaded)
        logger.info(f"Downloaded batch {i // BATCH_SIZE + 1}: {len(downloaded)} images")


@shared_task(
    bind=True,
    autoretry_for=(aiohttp.ClientError, asyncio.TimeoutError),
    retry_backoff=True,
    retry_backoff_max=300,
    retry_jitter=True,
    max_retries=5,
    rate_limit="30/m",
)
def find_images(self, episode_id: str, force: bool = False):
    """
    Find images for a specific episode and create or update Image objects
    Usage: from apps.tasks import find_images as t;t(Episode.objects.first().id);
    """
    run_async(process_images(episode_id, force=force))


async def process_download_images(images_id_list: list):
    """批量下载指定图片"""
    images = await sync_to_async(
        lambda: list(
            Image.objects.filter(id__in=images_id_list)
            .only("id", "raw_url")
            .values_list("id", "raw_url")
        )
    )()

    if not images:
        return

    # 分批并发下载
    for i in range(0, len(images), BATCH_SIZE):
        batch = [(str(id_), url) for id_, url in images[i : i + BATCH_SIZE]]
        downloaded = await download_batch_concurrent(batch)
        await save_images_batch(downloaded)


@shared_task(
    bind=True,
    autoretry_for=(Exception,),
    retry_backoff=True,
    max_retries=3,
)
def download_images(self, images_id_list: list):
    """
    Download images by ID list
    Usage: from apps.tasks import download_images as t;t([1,2,3]);
    """
    run_async(process_download_images(images_id_list))


# ============== 修复任务 ==============


@celery_app.task(
    base=QueueOnce,
    once={"graceful": True, "timeout": 60 * 60},
    soft_time_limit=55 * 60,
    time_limit=60 * 60,
)
def fix_images():
    """
    Fix missing images for Book and Image objects
    Usage: from apps.tasks import fix_images as t;t();
    """
    # 修复 Book 封面图
    books_to_fix = list(
        Book.objects.filter(image="").only("id", "image_url")[:100]
    )

    if books_to_fix:
        books_to_update = []
        for book in books_to_fix:
            try:
                image_data = run_async(
                    ImageExtractor().download_image(book.image_url)
                )
                if image_data:
                    book.image = base64.b64encode(image_data).decode()
                    books_to_update.append(book)
            except Exception as e:
                logger.error(f"Error fixing book {book.id}: {e}")

        if books_to_update:
            with transaction.atomic():
                Book.objects.bulk_update(books_to_update, ["image"], batch_size=50)
            logger.info(f"Fixed {len(books_to_update)} book covers")

    # 修复 Image - 分批调度任务
    missing_image_ids = list(
        Image.objects.filter(image="")
        .only("id")
        .values_list("id", flat=True)[:500]
    )

    if missing_image_ids:
        # 分批调度，每批 50 个
        for i in range(0, len(missing_image_ids), 50):
            batch = missing_image_ids[i : i + 50]
            download_images.apply_async(
                args=[batch],
                countdown=i // 50 * 10,  # 每批间隔 10 秒
            )
        logger.info(f"Scheduled {len(missing_image_ids)} images for download")


# ============== PDF 转换任务 ==============


async def process_convert_to_pdf(episode_id: str, force: bool = False):
    """处理 PDF 转换"""
    try:
        episode = await sync_to_async(Episode.objects.get)(pk=episode_id)
    except ObjectDoesNotExist:
        logger.error(f"Episode {episode_id} not found")
        return

    if episode.pdf and not force:
        return

    images = await sync_to_async(
        lambda: list(
            episode.images.all().order_by("index").values_list("image", flat=True)
        )
    )()
    images = [base64.b64decode(img) for img in images if img]

    if not images:
        logger.warning(f"No images for episode {episode_id}")
        return

    combined_image = await images_to_long_image(images, use_process_pool=False)
    if combined_image is None:
        logger.error(f"Failed to combine images for episode {episode_id}")
        return

    pdf_buffer = await long_image_to_pdf(combined_image, use_process_pool=False)

    if pdf_buffer:
        await sync_to_async(
            lambda: episode.pdf.save(
                f"{episode.title}.pdf", ContentFile(pdf_buffer.read())
            )
        )()
        await sync_to_async(episode.save)(update_fields=["pdf"])
        logger.info(f"Convert to PDF: {episode.title}")


@shared_task(
    bind=True,
    autoretry_for=(Exception,),
    retry_backoff=True,
    max_retries=2,
)
def convert_to_pdf(self, episode_id: str, force: bool = False):
    """
    Convert images of an episode to PDF
    Usage: from apps.tasks import convert_to_pdf as t;t(Episode.objects.first().id);
    """
    run_async(process_convert_to_pdf(episode_id, force))


@celery_app.task(
    base=QueueOnce,
    once={"graceful": True, "timeout": 60 * 60},
    soft_time_limit=55 * 60,
    time_limit=60 * 60,
)
def fix_pdf():
    """
    Fix missing PDFs for episodes
    Usage: from apps.tasks import fix_pdf as t;t();
    """
    # 使用更高效的查询
    image_subquery = Image.objects.filter(episode_id=OuterRef("pk"), image="")

    episode_ids = list(
        Episode.objects.filter(
            Q(pdf="") | Q(pdf__isnull=True),
        )
        .exclude(Exists(image_subquery))
        .filter(images__isnull=False)
        .distinct()
        .values_list("id", flat=True)[:100]
    )

    for i, episode_id in enumerate(episode_ids):
        convert_to_pdf.apply_async(
            args=[episode_id],
            countdown=i * 5,  # 每个任务间隔 5 秒
        )

    if episode_ids:
        logger.info(f"Scheduled {len(episode_ids)} episodes for PDF conversion")
