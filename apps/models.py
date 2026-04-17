# models.py
import base64
import logging

from asgiref.sync import sync_to_async
from django.core.files.base import ContentFile
from django.db import models
from PIL import ImageFile

from apps.tools import images_to_long_image, long_image_to_pdf

logger = logging.getLogger(__name__)

ImageFile.LOAD_TRUNCATED_IMAGES = True


class Tag(models.Model):
    name = models.CharField(max_length=100, unique=True, verbose_name="tag-name")

    class Meta:
        verbose_name = "Tag"
        verbose_name_plural = "Tags"
        ordering = ["name"]

    def __str__(self):
        return self.name


class Book(models.Model):
    id = models.CharField(max_length=100, unique=True, db_index=True, primary_key=True)
    image = models.TextField(default="")
    hot = models.IntegerField(default=0)
    title = models.CharField(max_length=100, default="")
    tags = models.ManyToManyField(Tag, related_name="books")
    description = models.TextField(default="")
    raw_url = models.URLField(default="")
    image_url = models.URLField(default="")

    class Meta:
        verbose_name = "Book"
        verbose_name_plural = "Books"
        ordering = ["title"]

    def __str__(self):
        return f"<{self.title}>"

    def is_outdated(self, episodes_title: str) -> bool:
        if not self.episodes.exists():
            return True
        return self.episodes.all().order_by("id").last().title != episodes_title


class Episode(models.Model):
    id = models.IntegerField(primary_key=True)
    title = models.CharField(max_length=100, default="")
    book = models.ForeignKey(Book, on_delete=models.CASCADE, related_name="episodes")
    raw_url = models.URLField(default="")
    pdf = models.FileField(upload_to="pdfs", null=True, blank=True)

    class Meta:
        verbose_name = "Episode"
        verbose_name_plural = "Episodes"
        ordering = ["book__id", "id", "title"]

    def __str__(self):
        return f"{self.book_id} - {self.id} - {self.title}"

    async def get_episode_long_image(self, auto_fix: bool = False):
        """获取拼接后的长图"""
        from apps.tasks import download_images, find_images

        # 只获取必要字段，避免加载大的 image 字段
        images = await sync_to_async(
            lambda: list(
                self.images.all()
                .order_by("index")
                .values_list("id", "image", named=False)
            )
        )()

        if not images:
            if auto_fix:
                find_images.apply_async(args=[self.id])
            return None

        # 检查是否有缺失图片
        missing_ids = [id_ for id_, img in images if not img]
        if missing_ids:
            logger.error(f"Episode {self.id} has {len(missing_ids)} missing images")
            if auto_fix:
                download_images.apply_async(args=[missing_ids], countdown=5)
            return None

        # 生成器方式处理，减少内存占用
        image_data = (base64.b64decode(img) for _, img in images)
        return await images_to_long_image(image_data)

    async def convert_to_pdf(self, force: bool = False, read: bool = False):
        """将 episode 转换为 PDF"""
        if self.pdf and not force:
            return await sync_to_async(self.pdf.read)() if read else None

        img = await self.get_episode_long_image(auto_fix=True)
        if not img:
            return None

        buffer = await long_image_to_pdf(img, use_process_pool=False)
        if buffer:
            await sync_to_async(self.pdf.save)(
                f"{self.title}.pdf", ContentFile(buffer.read())
            )

        return buffer


class ImageManager(models.Manager):
    """自定义 Manager，默认排除大字段"""

    def get_queryset(self):
        return super().get_queryset()

    def without_image_data(self):
        """不加载 image 字段的查询"""
        return self.defer("image")

    def with_image_data(self):
        """显式加载 image 字段"""
        return self.only("id", "episode_id", "index", "image", "raw_url")


class Image(models.Model):
    id = models.IntegerField(primary_key=True)
    episode = models.ForeignKey(
        Episode, on_delete=models.CASCADE, related_name="images"
    )
    index = models.IntegerField(default=0, db_index=True)
    image = models.TextField(default="")
    raw_url = models.URLField(default="")

    objects = ImageManager()

    class Meta:
        verbose_name = "Image"
        verbose_name_plural = "Images"
        ordering = ["episode__id", "index", "id"]
        indexes = [
            models.Index(fields=["episode", "index"]),
        ]

    def __str__(self):
        return f"Image {self.id} - Episode {self.episode_id} - Index {self.index}"
