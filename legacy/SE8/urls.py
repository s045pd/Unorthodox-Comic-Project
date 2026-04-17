from django.contrib import admin
from django.urls import include, path
from django_ratelimit.decorators import ratelimit

admin.site.site_header = "SE8 Admin"
admin.site.site_title = f"{admin.site.site_header} Admin Portal"
admin.site.index_title = f"Welcome to {admin.site.site_header}"


@ratelimit(key="ip", rate="6/m", block=True)
def extend_admin_login(request, extra_context=None):
    return admin.site.login(request, extra_context)


urlpatterns = [
    path("api/", include("apps.urls")),
    path("admin/", admin.site.urls),
    path("admin/login/", extend_admin_login),
    path("captcha/", include("captcha.urls")),
]
