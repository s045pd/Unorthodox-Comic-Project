from asyncio import get_event_loop
from logging import getLogger
from pathlib import Path
from random import choice

from django.core.management.base import BaseCommand, CommandParser

from apps.services import ImageExtractor
from apps.tools import images_to_long_image, long_image_to_pdf

logger = getLogger(__name__)


class Command(BaseCommand):
    help = "get random book"

    def add_arguments(self, parser: CommandParser) -> None:
        return super().add_arguments(parser)

    async def fetch_image(self, session, url):
        try:
            async with session.get(url) as response:
                return await response.read()
        except Exception as e:
            print(e)
            return None

    async def collect_async_generator(self, data):
        items = []
        async for item in data:
            items.append(item)
        return items

    async def handle_async(self):

        book_dir = Path("books")
        book_dir.mkdir(exist_ok=True)
        worker = ImageExtractor()
        await worker.get_max_page()

        random_page = choice(range(1, worker.max_page + 1))

        print("Start fetching books")
        if not (
            books := await self.collect_async_generator(
                worker.get_books(target_page=random_page)
            )
        ):
            print("No books found")
            return

        print(f"Got {len(books)} books")

        (book_dir / "name").write_text((book := choice(books))["id"])

        print(f"Getting book: {book['title']}")

        async for episode in worker.get_episodes(book["raw_url"]):
            try:
                if not episode.get("raw_url"):
                    continue

                print(f"Getting episode: {episode['title']}")
                image_urls = [
                    image["raw_url"]
                    async for image in worker.get_images(episode["raw_url"])
                ]
                images = await worker.get_images_concurrently(image_urls)

                print(f"Got {len(images)} images")
                if not (
                    long_image := await images_to_long_image(
                        [img for img in images if img]
                    )
                ):
                    continue

                print("Image: ", long_image, "Converting to PDF, please wait...")
                pdf_buffer = await long_image_to_pdf(long_image)
                with open(book_dir / f"{episode['title']}.pdf", "wb") as f:
                    f.write(pdf_buffer.read())
            except Exception as e:
                print("Error: ", e)

    def handle(self, *args, **options) -> None:
        loop = get_event_loop()
        loop.run_until_complete(self.handle_async())
