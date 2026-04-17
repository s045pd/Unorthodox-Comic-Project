import os

from celery import Celery

os.environ.setdefault("DJANGO_SETTINGS_MODULE", "SE8.settings")
app = Celery("se8")
app.config_from_object("django.conf:settings", namespace="CELERY")
app.autodiscover_tasks()
