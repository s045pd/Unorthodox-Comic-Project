class XFrameOptionsMiddleware:
    def __init__(self, get_response):
        self.get_response = get_response

    def __call__(self, request):
        response = self.get_response(request)
        if request.path.startswith('/admin/apps/episode/read/'):
            response['X-Frame-Options'] = 'SAMEORIGIN'
        return response