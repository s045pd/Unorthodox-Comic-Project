# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

SE8-Reader is a Django-based web scraper for downloading and managing comic content. It uses Celery for background task processing and provides a Django admin interface for content management.

## Development Commands

```bash
# Install dependencies
pip install -r requirements.txt

# Run development server
python manage.py runserver

# Run migrations
python manage.py migrate

# Start Celery worker (required for background tasks)
celery -A SE8 worker --loglevel=info

# Start Celery beat scheduler (for periodic tasks)
celery -A SE8 beat --loglevel=info

# Run tests
python manage.py test

# Collect static files
python manage.py collectstatic --noinput

# Create superuser
python manage.py createsuperuser
```

## Docker Quick Start

```bash
# Full stack with PostgreSQL and Redis
docker-compose up

# SQLite mode (simpler, for quick testing)
export USE_SQLITE=True && bash docker_image_rebuild.sh
```

## Architecture

### Core Components

- **SE8/** - Django project configuration (settings, celery, urls)
- **apps/** - Single Django app containing all business logic
  - `models.py` - Data models: Book, Episode, Image, Tag
  - `tasks.py` - Celery tasks for scraping and processing
  - `services.py` - `ImageExtractor` class handles web scraping
  - `admin.py` - Django admin configuration with custom actions
  - `tools.py` - Image processing utilities (PDF conversion, image stitching)

### Data Flow

1. `find_books` task crawls book listings from source site
2. `find_episodes` task fetches episode list for each book
3. `find_images` task downloads images for each episode
4. `convert_to_pdf` task stitches images into PDF files

### Background Tasks (Celery)

Key tasks in `apps/tasks.py`:
- `find_books()` - Discover new books (runs daily at midnight)
- `find_episodes(book_id)` - Get episodes for a book
- `find_images(episode_id)` - Download episode images
- `convert_to_pdf(episode_id)` - Generate PDF from images
- `fix_images()` - Repair missing images (runs daily at 1am)
- `fix_pdf()` - Generate missing PDFs (runs daily at 2am)

### Configuration

Environment variables (configured via `.env`):
- `USE_SQLITE` - Use SQLite instead of PostgreSQL
- `DB_*` - PostgreSQL connection settings
- `REDIS_*` - Redis connection settings
- `DEBUG` - Enable Django debug mode

SQLite mode automatically switches Celery broker and cache to file-based backends.

### Key Patterns

- Async code uses `async_event_loop()` context manager with Celery tasks
- `ImageExtractor` is a singleton for web scraping
- Images stored as base64 in database TextField
- Tasks use `QueueOnce` to prevent duplicate execution
