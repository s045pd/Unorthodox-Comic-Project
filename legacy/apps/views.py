import asyncio
import logging

from asgiref.sync import sync_to_async
from django.http import HttpResponse, JsonResponse
from django.shortcuts import get_object_or_404, render
from django.utils.decorators import method_decorator
from django.views import View
from django.views.decorators.csrf import csrf_exempt

from apps.models import Episode
from apps.tasks import find_books

logger = logging.getLogger(__name__)


@method_decorator(csrf_exempt, name="dispatch")
class TriggerFindBooksView(View):
    def post(self, request, *args, **kwargs):
        find_books.apply_async()
        return JsonResponse({"status": "success"})


def serve_pdf(request, episode_id):
    try:
        episode = get_object_or_404(Episode, pk=episode_id)
        buffer = asyncio.run(episode.convert_to_pdf(read=True))
        response = HttpResponse(buffer, content_type="application/pdf")
        response["Content-Disposition"] = f'attachment; filename="{episode.title}.pdf"'
        return response

    except Exception as e:
        logger.error(f"Error generating PDF for episode {episode_id}: {str(e)}")
        return HttpResponse("An error occurred while generating the PDF", status=500)


def read_episode_view(request, episode_id):
    episode = get_object_or_404(Episode, id=episode_id)
    book = episode.book

    previous_episode = (
        Episode.objects.filter(book=book, id__lt=episode_id).order_by("-id").first()
    )
    next_episode = (
        Episode.objects.filter(book=book, id__gt=episode_id).order_by("id").first()
    )

    context = {
        "episode": episode,
        "pdf_url": f"/api/episode/{episode.id}/pdf/",
        "previous_episode": previous_episode,
        "next_episode": next_episode,
    }
    return render(request, "admin/read_episode.html", context)
