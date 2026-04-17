import asyncio
from typing import AsyncGenerator, List, Optional

import aiohttp
from requests_html import HTML


class HTTPClientManager:
    """全局 HTTP 客户端管理器，复用连接池"""

    _instance: Optional["HTTPClientManager"] = None
    _session: Optional[aiohttp.ClientSession] = None
    _lock: Optional[asyncio.Lock] = None

    def __new__(cls):
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def __init__(self):
        if self._lock is None:
            self._lock = asyncio.Lock()

    async def get_session(self) -> aiohttp.ClientSession:
        """获取或创建 aiohttp session"""
        async with self._lock:
            if self._session is None or self._session.closed:
                connector = aiohttp.TCPConnector(
                    limit=100,  # 连接池大小
                    limit_per_host=20,  # 每个 host 的连接数
                    ttl_dns_cache=300,  # DNS 缓存 5 分钟
                    keepalive_timeout=60,
                )
                timeout = aiohttp.ClientTimeout(total=60)
                self._session = aiohttp.ClientSession(
                    connector=connector,
                    timeout=timeout,
                    headers={
                        "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:91.0) Gecko/20100101 Firefox/91.0",
                        "Accept-Language": "en-GB,en;q=0.9,zh-CN;q=0.8,zh;q=0.7",
                        "Cache-Control": "max-age=0",
                        "Dnt": "1",
                        "Priority": "u=0, i",
                    },
                )
            return self._session

    async def close(self):
        """关闭 session"""
        async with self._lock:
            if self._session and not self._session.closed:
                await self._session.close()
                self._session = None


# 全局 HTTP 客户端实例
http_client = HTTPClientManager()


class ImageExtractor:
    """图片提取器，使用全局 HTTP 客户端"""

    def __init__(self):
        self.origin = "https://se8.us"
        self.max_page = 2000

    async def _send_request(self, url: str) -> HTML:
        """使用 aiohttp 发送请求"""
        url = url.strip()
        session = await http_client.get_session()

        async with session.get(url, headers={"referer": "https://se8.us/"}) as response:
            response.raise_for_status()
            html_text = await response.text()
            return HTML(html=html_text)

    async def get_max_page(self) -> int:
        """Fetch the maximum page number"""
        try:
            resp = await self._send_request(f"{self.origin}/index.php/category/page/1")
            self.max_page = int(resp.xpath('//a[@class="end"]/@href')[0].split("/")[-1])
            print(f"Max page: {self.max_page}")
        except Exception as e:
            print(e)

    async def get_books(self, target_page: int = None) -> AsyncGenerator[str, None]:
        """Fetch books from the website"""

        page_range = (
            range(1, self.max_page + 1)
            if not target_page
            else range(target_page, target_page + 1)
        )

        for page in page_range:
            print(f"Fetching page {page}")
            resp = await self._send_request(
                f"{self.origin}/index.php/category/page/{page}"
            )
            if not (books := resp.xpath("//div[@class='common-comic-item']")):
                break

            for book in books:
                yield {
                    "raw_url": (url := book.xpath('//a[@class="cover"]/@href')[0]),
                    "id": url.split("/")[-1],
                    "title": book.xpath('//p[@class="comic__title"]')[0].text,
                    "image_url": book.xpath("//img/@data-original")[0],
                    "current": book.xpath("//p[@class='comic-update']/a/text()")[0],
                }

    async def get_episodes(self, url: str) -> AsyncGenerator[str, None]:
        """Fetch episodes for a specific book"""
        resp = await self._send_request(url)
        if not (
            episodes := resp.xpath("//ul[@class='chapter__list-box clearfix']//li")
        ):
            return

        yield {
            "tags": resp.xpath("//div[@class='comic-status']//a/text()"),
            "hot": float(
                resp.xpath("//div[@class='comic-status']/span[3]/b/text()")[0].split()[
                    0
                ]
            ),
            "description": resp.xpath("//div[@class='comic-intro']//p")[2].text,
        }

        for episode in episodes:
            yield {
                "raw_url": (url := episode.xpath("//a/@href")[0]),
                "id": url.split("/")[-1],
                "title": episode.text,
            }

    async def get_images(self, url: str) -> AsyncGenerator:
        """Fetch images for a specific episode"""
        resp = await self._send_request(url)
        if not (images := resp.xpath("//div[@class='rd-article__pic hide']")):
            return

        for image_div in images:
            yield {
                "id": image_div.xpath("//@data-pid")[0],
                "index": image_div.xpath("//@data-index")[0],
                "raw_url": image_div.xpath("//img/@data-original")[0],
            }

    async def download_image(self, url: str, key: str = None) -> bytes | tuple | str:
        """Download image from the given URL using global HTTP client"""
        try:
            session = await http_client.get_session()
            async with session.get(
                url, headers={"referer": "https://se8.us/"}
            ) as response:
                if response.status != 200:
                    return ""
                content_type = response.headers.get("Content-Type", "")
                if not content_type.startswith("image"):
                    return ""
                content = await response.read()
                if key:
                    return (key, content)
                return content
        except Exception:
            return ""

    async def get_images_concurrently(self, urls: List[str]) -> List[bytes]:
        """Fetch images concurrently with semaphore control"""
        semaphore = asyncio.Semaphore(20)

        async def download_with_semaphore(url: str) -> bytes:
            async with semaphore:
                return await self.download_image(url)

        tasks = [download_with_semaphore(url) for url in urls]
        return await asyncio.gather(*tasks)

    async def get_images_concurrently_with_id(
        self, items: List[tuple]
    ) -> List[tuple]:
        """Fetch images concurrently with id"""
        semaphore = asyncio.Semaphore(20)

        async def download_with_semaphore(key: str, url: str) -> tuple:
            async with semaphore:
                return await self.download_image(url, key)

        tasks = [download_with_semaphore(key, url) for (key, url) in items]
        return await asyncio.gather(*tasks)
