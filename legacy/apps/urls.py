from django.urls import path

from apps.views import TriggerFindBooksView, read_episode_view, serve_pdf

urlpatterns = [
    path("episode/<int:episode_id>/pdf/", serve_pdf, name="serve_pdf"),
    path("episode/<int:episode_id>/", read_episode_view, name="read_episode_view"),
    path(
        "trigger-find-books/", TriggerFindBooksView.as_view(), name="trigger_find_books"
    ),
]
