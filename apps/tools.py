import asyncio
import atexit
from concurrent.futures import ProcessPoolExecutor, ThreadPoolExecutor
from io import BytesIO
from logging import getLogger

from PIL import Image as PILImage
from reportlab.lib.pagesizes import A4
from reportlab.lib.utils import ImageReader
from reportlab.pdfgen import canvas

logger = getLogger(__name__)

# ============== 全局 Executor 池 ==============
_thread_pool = None
_process_pool = None


def get_thread_pool(max_workers: int = 4) -> ThreadPoolExecutor:
    """获取全局线程池"""
    global _thread_pool
    if _thread_pool is None:
        _thread_pool = ThreadPoolExecutor(max_workers=max_workers)
        atexit.register(_thread_pool.shutdown, wait=False)
    return _thread_pool


def get_process_pool(max_workers: int = 2) -> ProcessPoolExecutor:
    """获取全局进程池"""
    global _process_pool
    if _process_pool is None:
        _process_pool = ProcessPoolExecutor(max_workers=max_workers)
        atexit.register(_process_pool.shutdown, wait=False)
    return _process_pool


# ============== 图片处理函数 ==============


def load_and_convert_image(image):
    """加载并转换图片为 RGB 模式"""
    try:
        img = PILImage.open(BytesIO(image))
        if img.mode != "RGB":
            img = img.convert("RGB")
        return img
    except Exception as e:
        logger.error(f"Error loading image: {str(e)}")
        return None


def combine_images(images):
    """将多张图片拼接成长图"""
    # Process and combine images
    image_list = []
    total_height = 0
    max_width = 0

    for image in images:
        img = load_and_convert_image(image)
        if img is not None:
            image_list.append(img)
            total_height += img.height
            max_width = max(max_width, img.width)

    if not image_list:
        return None

    # Combine all images into one long image
    combined_image = PILImage.new("RGB", (max_width, total_height))
    y_offset = 0
    for img in image_list:
        combined_image.paste(img, (0, y_offset))
        y_offset += img.height
        img.close()  # 及时释放内存

    return combined_image


def create_pdf(img):
    """将长图转换为 PDF"""
    if img is None:
        return None

    # Calculate dimensions and scaling
    img_width, img_height = img.size
    pdf_width, pdf_height = A4
    scale = pdf_width / img_width
    scaled_height = int(img_height * scale)
    pages = (scaled_height + int(pdf_height) - 1) // int(pdf_height)

    # Create PDF
    buffer = BytesIO()
    pdf_canvas = canvas.Canvas(buffer, pagesize=A4)

    for page in range(pages):
        # Calculate crop box for each page
        top = int(page * pdf_height / scale)
        bottom = int((page + 1) * pdf_height / scale)
        bottom = min(bottom, img_height)

        if top >= bottom:
            logger.error(f"Invalid crop box coordinates: top={top}, bottom={bottom}")
            continue

        # Crop and resize image for the current page
        crop_box = (0, top, img_width, bottom)
        cropped_img = img.crop(crop_box)

        new_width = int(pdf_width)
        new_height = int(cropped_img.height * scale)

        if new_width <= 0 or new_height <= 0:
            logger.error(
                f"Invalid dimensions for resized image: width={new_width}, height={new_height}. "
                f"Original crop box: top={top}, bottom={bottom}, "
                f"cropped_img.height={cropped_img.height}, scale={scale}"
            )
            cropped_img.close()
            continue

        cropped_img = cropped_img.resize((new_width, new_height))

        # Save cropped image to buffer and draw on PDF
        img_buffer = BytesIO()
        cropped_img.save(img_buffer, format="PNG")
        img_buffer.seek(0)

        pdf_canvas.drawImage(
            ImageReader(img_buffer),
            0,
            pdf_height - cropped_img.height,
            width=pdf_width,
            height=cropped_img.height,
        )

        cropped_img.close()  # 及时释放内存
        pdf_canvas.showPage()

    pdf_canvas.save()

    # Save PDF to model
    buffer.seek(0)

    return buffer


# ============== 异步包装函数 ==============


async def images_to_long_image(images, use_process_pool=False):
    """异步拼接图片，使用全局 Executor 池"""
    loop = asyncio.get_event_loop()
    executor = get_process_pool() if use_process_pool else get_thread_pool()
    # 需要将生成器转换为列表
    images_list = list(images) if not isinstance(images, list) else images
    return await loop.run_in_executor(executor, combine_images, images_list)


async def long_image_to_pdf(img, use_process_pool=False):
    """异步生成 PDF，使用全局 Executor 池"""
    loop = asyncio.get_event_loop()
    executor = get_process_pool() if use_process_pool else get_thread_pool()
    return await loop.run_in_executor(executor, create_pdf, img)
