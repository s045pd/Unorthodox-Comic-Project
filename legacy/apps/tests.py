from django.test import TestCase


def test_get_images():
    from apps.services import ImageExtractor

    extractor = ImageExtractor("https://se8.us/index.php/chapter/12310")
    images = extractor.get_images()
    assert len(images) > 0
