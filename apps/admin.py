from django.contrib import admin
from django.db.models import Count, Q
from django.utils.html import format_html

from apps.models import Book, Episode, Image, Tag
from apps.tasks import convert_to_pdf, download_images, find_episodes, find_images


@admin.register(Tag)
class TagAdmin(admin.ModelAdmin):
    list_display = ("name", "book_count")

    def get_queryset(self, request):
        """预计算书籍数量"""
        queryset = super().get_queryset(request)
        queryset = queryset.annotate(book_count=Count("books"))
        return queryset

    def book_count(self, obj):
        """使用预计算的书籍数量"""
        return obj.book_count

    book_count.short_description = "Number of Books"
    book_count.admin_order_field = "book_count"


@admin.register(Book)
class BookAdmin(admin.ModelAdmin):
    list_display = ("id", "title", "episode_count", "hot", "view_episodes")
    search_fields = ("title", "id")
    list_filter = ("tags",)
    readonly_fields = (
        "title",
        "id",
        "raw_url",
        "image_url",
        "image",
        "description",
        "hot",
        "tags",
    )
    actions = ["start_crawling"]

    def get_queryset(self, request):
        """预计算章节数量"""
        queryset = super().get_queryset(request)
        queryset = queryset.annotate(episode_count=Count("episodes"))
        return queryset

    def episode_count(self, obj):
        """使用预计算的章节数量"""
        return obj.episode_count

    episode_count.short_description = "Episodes"
    episode_count.admin_order_field = "episode_count"

    def view_episodes(self, obj):
        """Generate a link to view episodes of this book"""
        return format_html(
            '<a class="button" href="{}">Read</a>',
            f"/admin/apps/episode/?book__id__exact={obj.id}",
        )

    view_episodes.short_description = "View Episodes"

    def start_crawling(self, request, queryset):
        """Start crawling episodes for selected books"""
        for book in queryset.only("id"):
            find_episodes.apply_async(args=[book.id])

    start_crawling.short_description = "Start Crawling"


@admin.register(Episode)
class EpisodeAdmin(admin.ModelAdmin):
    list_display = (
        "id",
        "title",
        "book",
        "image_stats",
        "view_images",
        "all_images",
        "has_pdf",
        "read_episode",
    )
    search_fields = ("title", "book__title")
    list_filter = ("book__tags", "book__title")
    readonly_fields = ("book", "title", "id", "raw_url")
    actions = ["get_images", "do_convert_to_pdf", "convert_to_pdf_force", "refresh_images"]

    def get_queryset(self, request):
        """预计算图片统计，避免 N+1 查询"""
        queryset = super().get_queryset(request)
        queryset = queryset.select_related("book")
        queryset = queryset.annotate(
            total_images=Count("images"),
            completed_images=Count("images", filter=~Q(images__image="")),
        )
        return queryset

    def image_stats(self, obj):
        """使用预计算的图片统计"""
        return f"{obj.completed_images}/{obj.total_images}"

    image_stats.short_description = "Images"
    image_stats.admin_order_field = "completed_images"

    def all_images(self, obj):
        """使用预计算值判断图片是否完整"""
        return obj.completed_images == obj.total_images and obj.total_images > 0

    all_images.short_description = "Complete"
    all_images.boolean = True

    def has_pdf(self, obj):
        """Check if this episode has a PDF"""
        return bool(obj.pdf)

    has_pdf.short_description = "PDF"
    has_pdf.boolean = True

    def read_episode(self, obj):
        """Generate a link to read this episode"""
        return format_html(
            '<a class="button" href="{}">Read</a>', f"/api/episode/{obj.id}/"
        )

    read_episode.short_description = "Read"

    def view_images(self, obj):
        """Generate a link to view images of this episode"""
        return format_html(
            '<a class="button" href="{}">View</a>',
            f"/admin/apps/image/?episode__id__exact={obj.id}",
        )

    view_images.short_description = "Images"

    def do_convert_to_pdf(self, request, queryset):
        """Convert selected episodes to PDF"""
        for episode_id in queryset.values_list("id", flat=True):
            convert_to_pdf.apply_async(args=[episode_id])

    do_convert_to_pdf.short_description = "Convert to PDF"

    def convert_to_pdf_force(self, request, queryset):
        """Force convert selected episodes to PDF"""
        for episode_id in queryset.values_list("id", flat=True):
            convert_to_pdf.apply_async(args=[episode_id, True])

    convert_to_pdf_force.short_description = "Convert to PDF (Force)"

    def get_images(self, request, queryset):
        """Download images for selected episodes"""
        for episode_id in queryset.values_list("id", flat=True):
            find_images.apply_async(args=[episode_id, True])

    get_images.short_description = "Download Images (Force)"

    def refresh_images(self, request, queryset):
        """Refresh images for selected episodes"""
        for episode_id in queryset.values_list("id", flat=True):
            find_images.apply_async(args=[episode_id])

    refresh_images.short_description = "Find & Download Images"


@admin.register(Image)
class ImageAdmin(admin.ModelAdmin):
    list_display = ("id", "episode", "index", "has_image")
    search_fields = ("episode__title", "id")
    list_filter = ("episode__book",)
    readonly_fields = ("episode", "index", "id", "raw_url")
    actions = ["get_images"]

    # 不在列表页显示图片预览，太慢
    # 如需查看图片，点击进入详情页

    def has_image(self, obj):
        """显示是否有图片，而不是显示图片本身"""
        return bool(obj.image)

    has_image.short_description = "Has Image"
    has_image.boolean = True

    def get_queryset(self, request):
        """优化查询，排除大字段"""
        queryset = super().get_queryset(request)
        queryset = queryset.select_related("episode", "episode__book")
        # 列表页不加载 image 字段（太大）
        queryset = queryset.defer("image")
        return queryset

    def get_images(self, request, queryset):
        """Download images for selected images"""
        image_ids = list(queryset.values_list("id", flat=True))
        if image_ids:
            download_images.apply_async(args=[image_ids])

    get_images.short_description = "Download Images (Force)"
