# SE8-Reader — Go 重写设计

- **日期**：2026-04-17
- **状态**：已批准设计，待实施
- **作者**：s045pd (with Claude)
- **原项目**：Django 4.2 + Celery + PostgreSQL/SQLite + Redis
- **目标**：单二进制 Go 服务，SQLite + 文件系统，NAS 友好

---

## 目录

1. [动机与关键决策](#1-动机与关键决策)
2. [整体架构](#2-整体架构)
3. [目录结构](#3-目录结构)
4. [数据模型](#4-数据模型)
5. [核心流程](#5-核心流程)
6. [Web UI + API](#6-web-ui--api)
7. [配置、迁移、构建与部署](#7-配置迁移构建与部署)
8. [测试策略](#8-测试策略)
9. [附录：决策记录](#9-附录决策记录)

---

## 1. 动机与关键决策

### 1.1 重写动机

**部署简化**。现状需维护多个进程：PostgreSQL、Redis、Celery worker、Celery beat、Gunicorn。目标是将其压缩为**单一 Go 二进制 + SQLite 数据文件 + 媒体目录**，便于在 NAS 上长期运行。

### 1.2 关键决策速览

| 维度 | 选择 | 理由 |
|---|---|---|
| 运行形态 | 单二进制单进程 | 消除 5 个常驻进程的运维负担 |
| 数据库 | SQLite（`modernc.org/sqlite` 纯 Go 驱动） | 单文件备份、无 CGO、可交叉编译 |
| 后台任务 | 协程 + SQLite `jobs` 表持久化 | 崩溃安全、可观测（UI 可见任务状态） |
| 调度 | `robfig/cron/v3` | 取代 Celery Beat，功能等价 |
| 图片存储 | 文件系统（`vol/media/books/{bid}/{eid}/{idx}.{ext}`） | 避免 DB 膨胀，备份更灵活 |
| 管理界面 | 内置轻量 Web UI（`html/template` + HTMX + Pico.css） | 无需前端构建，保留 Admin 的点击浏览体验 |
| 认证 | 单管理员账号 + Cookie Session + bcrypt | NAS 部署下的最小安全面 |
| 旧数据 | 一次性迁移脚本 | 保留历史爬取成果 |

---

## 2. 整体架构

**单一 Go 二进制进程**，内部并行运行四类协程：

```
                    ┌────────────────────────────────────┐
                    │         se8 (single binary)        │
                    │                                    │
  HTTP :8000  ─────→│  ① Web Server (chi + html/template)│
                    │     └ Admin UI + JSON APIs         │
                    │                                    │
  cron tick  ──────→│  ② Scheduler (robfig/cron/v3)      │
                    │     └ enqueue find_books daily     │
                    │                                    │
                    │  ③ Job Runner (N worker goroutines)│
                    │     ├ polls SQLite `jobs` table    │
                    │     ├ runs crawler / image / pdf   │
                    │     └ updates status + retries     │
                    │                                    │
                    │  ④ Crawler client (net/http pool)  │
                    │     └ shared by all workers        │
                    └────────────────────────────────────┘
                                    │
                     ┌──────────────┴──────────────┐
                     │                             │
                     ▼                             ▼
              ┌─────────────┐            ┌──────────────────┐
              │ SQLite DB   │            │ Filesystem       │
              │ (vol/se8.db)│            │ vol/media/books/ │
              │             │            │  {bid}/{eid}/    │
              │ books       │            │    001.jpg       │
              │ episodes    │            │    002.jpg       │
              │ images      │            │                  │
              │ tags        │            │ vol/media/pdfs/  │
              │ jobs        │            │  {eid}.pdf       │
              │ users       │            │                  │
              │ sessions    │            └──────────────────┘
              └─────────────┘
```

**关键原则**：

- **四类协程全在同一进程内**，通过共享 `*sql.DB` 和 `context.Context` 协作
- **优雅关停**：SIGTERM → `ctx.Cancel()` → 等 worker 完成当前任务最多 30s → 退出
- **崩溃恢复**：启动时把 `jobs` 表里 `status=running` 的记录重置为 `pending`，假设上次进程异常退出

### 2.1 技术栈

| 层 | 库 | 备注 |
|---|---|---|
| Web 框架 | `github.com/go-chi/chi/v5` | 标准 `net/http` 风格 |
| ORM | `sqlc-dev/sqlc`（代码生成） | 类型安全，零运行时开销 |
| SQLite 驱动 | `modernc.org/sqlite` | 纯 Go，无 CGO |
| HTML 解析 | `github.com/PuerkitoBio/goquery` | jQuery 风格 CSS 选择器 |
| 调度 | `github.com/robfig/cron/v3` | cron 表达式 |
| 图片处理 | `github.com/disintegration/imaging` | 拼接、缩放 |
| PDF 生成 | `github.com/go-pdf/fpdf` | A4 分页绘制 |
| 密码哈希 | `golang.org/x/crypto/bcrypt` | — |
| 并发控制 | `golang.org/x/sync/semaphore` | 下载/任务并发上限 |
| 日志 | 标准库 `log/slog` + `natefinch/lumberjack` | 结构化 JSON + 轮转 |
| 配置 | `github.com/caarlos0/env/v10` + `joho/godotenv` | `.env` + 环境变量 |
| 前端 | HTMX 1.x + Pico.css（嵌入 `embed.FS`） | 无构建 |

---

## 3. 目录结构

```
se8/
├── cmd/
│   ├── se8/              # 主服务入口
│   │   └── main.go
│   └── migrate/          # 一次性数据迁移工具
│       └── main.go
├── internal/
│   ├── config/           # 环境变量加载、默认值
│   │   └── config.go
│   ├── storage/          # SQLite 访问层
│   │   ├── queries.sql   # sqlc 输入（SELECT/INSERT/UPDATE 语句）
│   │   ├── db.go         # *sql.DB 打开 + 启动时按序执行 migrations/*.sql
│   │   ├── migrations/   # 演进式 schema（001_init.sql, 002_*.sql ...，本次首发只有 001）
│   │   └── generated/    # sqlc 产物（不手改）
│   ├── domain/           # 领域模型
│   │   └── types.go
│   ├── crawler/          # 网页抓取
│   │   ├── client.go     # 带连接池的 HTTP 客户端
│   │   ├── extractor.go  # goquery 解析书/章/图
│   │   └── download.go   # 并发图片下载（带 semaphore）
│   ├── imaging/          # 图片拼接与 PDF 生成
│   │   ├── combine.go
│   │   └── pdf.go
│   ├── jobs/             # 任务队列
│   │   ├── types.go      # Job 定义 + TaskKey 去重键
│   │   ├── queue.go      # 入队/出队/重试逻辑
│   │   ├── runner.go     # worker goroutine 池
│   │   └── handlers.go   # 注册 find_books / find_episodes / ... 处理器
│   ├── scheduler/        # 定时任务
│   │   └── scheduler.go  # cron → enqueue job
│   ├── auth/             # 账号、Session、密码哈希
│   │   ├── password.go
│   │   └── session.go
│   └── web/              # HTTP 层
│       ├── router.go
│       ├── middleware.go # logging/auth/ratelimit
│       ├── handlers/
│       │   ├── books.go
│       │   ├── episodes.go
│       │   ├── images.go
│       │   ├── jobs.go
│       │   └── auth.go
│       ├── templates/    # html/template（embed.FS）
│       │   ├── layout.html
│       │   ├── login.html
│       │   ├── books_list.html
│       │   ├── book_detail.html
│       │   ├── episodes_list.html
│       │   ├── episode_read.html
│       │   ├── images_list.html
│       │   ├── tags_list.html
│       │   └── jobs.html
│       └── static/       # htmx.min.js + pico.css（embed.FS）
├── vol/                  # 运行时数据目录（.gitignore）
│   ├── se8.db
│   ├── first-run-password.txt
│   ├── media/
│   │   ├── covers/{bid}.jpg
│   │   ├── books/{bid}/{eid}/NNN.jpg
│   │   └── pdfs/{eid}.pdf
│   └── logs/
├── docs/
│   └── superpowers/specs/
├── .env.example
├── go.mod
├── Makefile
├── Dockerfile
└── README.md
```

**要点**：

- `cmd/` 两个二进制：主服务 + 迁移工具，共享 `internal/*`
- `internal/web/templates/` + `static/` 用 `//go:embed` 打包进二进制
- `vol/` 单一目录承载所有运行态数据（db + 图片 + PDF + 日志），便于 NAS 挂卷备份
- sqlc 生成代码隔离到 `internal/storage/generated/`，避免手改

---

## 4. 数据模型

### 4.1 SQLite Schema

```sql
-- ================= 业务领域 =================
CREATE TABLE books (
    id            TEXT PRIMARY KEY,
    title         TEXT NOT NULL DEFAULT '',
    description   TEXT NOT NULL DEFAULT '',
    hot           INTEGER NOT NULL DEFAULT 0,
    raw_url       TEXT NOT NULL DEFAULT '',
    image_url     TEXT NOT NULL DEFAULT '',
    cover_path    TEXT NOT NULL DEFAULT '',   -- 相对 vol/
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);

CREATE TABLE episodes (
    id            INTEGER PRIMARY KEY,
    book_id       TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    title         TEXT NOT NULL DEFAULT '',
    raw_url       TEXT NOT NULL DEFAULT '',
    pdf_path      TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);
CREATE INDEX idx_episodes_book ON episodes(book_id);

CREATE TABLE images (
    id            INTEGER PRIMARY KEY,
    episode_id    INTEGER NOT NULL REFERENCES episodes(id) ON DELETE CASCADE,
    idx           INTEGER NOT NULL DEFAULT 0,
    raw_url       TEXT NOT NULL DEFAULT '',
    file_path     TEXT NOT NULL DEFAULT '',   -- 空串 = 未下载
    width         INTEGER NOT NULL DEFAULT 0,
    height        INTEGER NOT NULL DEFAULT 0,
    bytes         INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);
CREATE INDEX idx_images_episode ON images(episode_id, idx);
CREATE INDEX idx_images_missing ON images(file_path) WHERE file_path = '';

CREATE TABLE tags (
    id    INTEGER PRIMARY KEY AUTOINCREMENT,
    name  TEXT NOT NULL UNIQUE
);

CREATE TABLE book_tags (
    book_id  TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    tag_id   INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (book_id, tag_id)
);

-- ================= 任务队列 =================
CREATE TABLE jobs (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    kind          TEXT NOT NULL,
    payload       TEXT NOT NULL DEFAULT '{}',
    task_key      TEXT NOT NULL UNIQUE,
    status        TEXT NOT NULL DEFAULT 'pending',  -- pending/running/done/failed
    priority      INTEGER NOT NULL DEFAULT 0,
    attempts      INTEGER NOT NULL DEFAULT 0,
    max_attempts  INTEGER NOT NULL DEFAULT 3,
    last_error    TEXT NOT NULL DEFAULT '',
    run_at        INTEGER NOT NULL,
    started_at    INTEGER,
    finished_at   INTEGER,
    created_at    INTEGER NOT NULL
);
CREATE INDEX idx_jobs_ready ON jobs(status, run_at) WHERE status = 'pending';
CREATE INDEX idx_jobs_history ON jobs(kind, finished_at);

-- ================= 认证 =================
CREATE TABLE users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    INTEGER NOT NULL
);

CREATE TABLE sessions (
    token       TEXT PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at  INTEGER NOT NULL,
    created_at  INTEGER NOT NULL
);
CREATE INDEX idx_sessions_expiry ON sessions(expires_at);
```

### 4.2 关键设计决策

1. **`task_key` UNIQUE** 实现 Celery `QueueOnce` 同等效果：同一 key 已 pending/running 时新入队使用 `INSERT OR IGNORE` 直接跳过
2. **延迟执行**用 `run_at` 支持——不再需要 `countdown`，等效 Celery 的 `apply_async(countdown=N)`
3. **图片的 `file_path`** 空串 = 未下载，`fix_images` 任务扫描 `WHERE file_path = ''`
4. **首次启动**自动创建默认用户 `admin`，密码随机生成 32 字节 hex，打印到 stdout + 写 `vol/first-run-password.txt`
5. **时间字段**统一 unix 秒 INTEGER，比 TEXT TIMESTAMP 快且省空间

---

## 5. 核心流程

### 5.1 任务队列（`internal/jobs/`）

```go
type JobKind string

const (
    KindFindBooks     JobKind = "find_books"
    KindFindEpisodes  JobKind = "find_episodes"
    KindFindImages    JobKind = "find_images"
    KindDownloadImage JobKind = "download_image"
    KindConvertPDF    JobKind = "convert_pdf"
    KindFixImages     JobKind = "fix_images"
    KindFixPDF        JobKind = "fix_pdf"
)

type Handler func(ctx context.Context, payload json.RawMessage) error

type Queue struct {
    db       *sql.DB
    handlers map[JobKind]Handler
}

// Enqueue 去重入队：同 task_key 已 pending/running 则跳过
func (q *Queue) Enqueue(ctx context.Context, kind JobKind, key string, payload any, opts ...Option) error

type Runner struct {
    queue        *Queue
    maxWorkers   int           // 默认 4
    pollInterval time.Duration // 2s
}
```

**执行流程**：

1. 启动时：`UPDATE jobs SET status='pending' WHERE status='running'`（崩溃恢复）
2. 每 2s 循环：`SELECT ... WHERE status='pending' AND run_at<=now() ORDER BY priority DESC, id LIMIT N` → 批量 UPDATE 为 running
3. 每条任务独立 goroutine 执行 → 成功 `status=done`；失败 `attempts++`，若 `attempts<max_attempts` 则回到 `pending` 且 `run_at=now()+exponential_backoff`，否则 `status=failed`
4. 信号量 `semaphore.NewWeighted(maxWorkers)` 控制同时运行数

### 5.2 Crawler（`internal/crawler/`）

```go
type Client struct {
    http *http.Client // MaxIdleConnsPerHost=20, Timeout=60s
}

type Extractor struct {
    client *Client
    origin string // https://se8.us
}

func (e *Extractor) GetBooks(ctx context.Context, page int) ([]BookDTO, error)
func (e *Extractor) GetEpisodes(ctx context.Context, url string) (BookMeta, []EpisodeDTO, error)
func (e *Extractor) GetImages(ctx context.Context, url string) ([]ImageDTO, error)

func (c *Client) DownloadImage(ctx context.Context, url string) ([]byte, string, error) // bytes, content-type
```

**XPath → CSS 选择器映射**（从原代码抽取）：

| 原 XPath | 新 CSS（goquery） |
|---|---|
| `//div[@class='common-comic-item']` | `div.common-comic-item` |
| `//a[@class="cover"]/@href` | `a.cover[href]` |
| `//p[@class="comic__title"]` | `p.comic__title` |
| `//img/@data-original` | `img[data-original]` |
| `//p[@class='comic-update']/a/text()` | `p.comic-update a` |
| `//ul[@class='chapter__list-box clearfix']//li` | `ul.chapter__list-box li` |
| `//div[@class='comic-status']//a/text()` | `div.comic-status a` |
| `//div[@class='comic-intro']//p` | `div.comic-intro p` |
| `//div[@class='rd-article__pic hide']` | `div.rd-article__pic.hide` |
| `//@data-pid` | `attr("data-pid")` |

**并发控制**：`semaphore.NewWeighted(20)` 控制同时下载数，与原 `MAX_CONCURRENT_REQUESTS=20` 对齐。

### 5.3 Imaging（`internal/imaging/`）

```go
// CombineImages 把 n 张图纵向拼接成长图
func CombineImages(images [][]byte) (image.Image, error)

// ToPDF 把长图切成 A4 页写成 PDF
func ToPDF(img image.Image, out io.Writer) error
```

- **不用临时文件**：`image.Decode` → 内存拼接 → 逐页切分写 PDF
- **大图保护**：拼接前累加高度，若超过 30000px 记日志但允许超过（对齐原行为，不截断）
- **并发**：每个 PDF 转换任务在自己 goroutine 里运行；Go 的 image + gofpdf 足够快，不额外做线程池

### 5.4 任务链路（对照原 Celery）

| 原 Celery | 新 Job Kind | 触发时机 | Task Key |
|---|---|---|---|
| `find_books` | `find_books` | 每天 00:00（cron） | `find_books:daily` |
| `find_episodes(book_id)` | `find_episodes` | `find_books` 发现新书时入队 | `find_episodes:{book_id}` |
| `find_images(episode_id)` | `find_images` | 新章节 + 用户手动 | `find_images:{episode_id}` |
| `download_images(ids)` | `download_image` | 单张图片下载（粒度变小） | `download_image:{image_id}` |
| `convert_to_pdf(episode_id)` | `convert_pdf` | 所有图下完自动 + 手动 | `convert_pdf:{episode_id}` |
| `fix_images` | `fix_images` | 每天 01:00 | `fix_images:daily` |
| `fix_pdf` | `fix_pdf` | 每天 02:00 | `fix_pdf:daily` |

**粒度变化**：`download_images` 原来一次批量下 50 张，新方案拆成单张任务。好处是失败重试不影响已下载的图，队列进度可见。

---

## 6. Web UI + API

### 6.1 路由表

```
公开（无需登录）
  GET  /login                → 登录页
  POST /login                → 登录提交
  GET  /healthz              → 健康检查

需登录（Cookie session）
  GET  /                     → 重定向到 /books
  POST /logout

  书籍
  GET  /books                → 书籍列表（分页、搜索、tag 筛选）
  GET  /books/{id}           → 书籍详情 + 章节列表
  POST /books/{id}/crawl     → 入队 find_episodes

  章节
  GET  /episodes             → 章节列表
  GET  /episodes/{id}        → 章节阅读页
  GET  /episodes/{id}/pdf    → 下载 PDF
  POST /episodes/{id}/fetch  → 入队 find_images
  POST /episodes/{id}/pdf    → 入队 convert_pdf（?force=1 强制重生成）

  图片
  GET  /images               → 图片列表
  GET  /media/*              → 静态文件（vol/media/）

  标签
  GET  /tags                 → 标签列表 + 书数

  任务
  GET  /jobs                 → 任务队列视图完整页面
  GET  /jobs/fragment        → 任务表 HTML 片段（HTMX 每 3s 轮询此端点）
  POST /jobs/{id}/retry
  DELETE /jobs/{id}

  系统
  POST /admin/start-crawl    → 手动触发 find_books

JSON API（同样走 session auth）
  GET  /api/books
  GET  /api/books/{id}
  GET  /api/episodes/{id}
  GET  /api/jobs
  POST /api/jobs             → {kind, payload}
```

### 6.2 页面清单

| 页面 | 主要组件 |
|---|---|
| `layout.html` | 顶部导航 + "Start Crawl" 按钮 |
| `login.html` | 极简登录表单 |
| `books_list.html` | 表格：封面缩略图 / 标题 / hot / 章节数 / tags / [Read] [Crawl] |
| `book_detail.html` | 头部信息 + 章节表 |
| `episodes_list.html` | 跨书章节表 |
| `episode_read.html` | 长列滚动阅读（`<img loading="lazy">`） |
| `images_list.html` | 表格：episode / index / has_file / bytes / [Redownload] |
| `tags_list.html` | 标签云 + 书数 |
| `jobs.html` | 任务表（HTMX 每 3s 轮询） |

### 6.3 HTMX 交互

```html
<!-- 任务表自动轮询 -->
<div id="jobs" hx-get="/jobs/fragment" hx-trigger="every 3s" hx-swap="innerHTML">
  ...
</div>

<!-- 爬取按钮不跳转，返回片段替换 -->
<button hx-post="/books/{{.ID}}/crawl" hx-swap="outerHTML">Crawl</button>
<!-- 服务端返回 <span>Queued ✓</span> -->
```

### 6.4 认证流程

1. 首次启动：users 表为空 → 创建 `admin` + 32 字节随机密码，打印到 stdout + 写 `vol/first-run-password.txt`
2. 登录 POST → bcrypt 比对 → 生成 32 字节 hex token → 写 sessions 表 → Set-Cookie `se8_session`（HttpOnly, SameSite=Lax, Path=/, 30 天）
3. 中间件：无 cookie 或 session 过期 → 302 `/login`；API 路径返回 401
4. 后台清理协程：每小时 `DELETE FROM sessions WHERE expires_at < now()`

**限速**：`httprate.LimitByIP(10, time.Minute)` 只套 `/login`。

---

## 7. 配置、迁移、构建与部署

### 7.1 配置（环境变量 / `.env`）

```bash
SE8_ADDR=0.0.0.0:8000
SE8_BASE_URL=https://se8.us
SE8_VOL_DIR=./vol

SE8_MAX_CONCURRENT_REQUESTS=20
SE8_WORKER_COUNT=4
SE8_MAX_PAGE=2000

SE8_HTTP_TIMEOUT=60s
SE8_SESSION_TTL=720h

SE8_LOG_LEVEL=info
SE8_LOG_FORMAT=text

SE8_DEBUG=false
```

用 `caarlos0/env` 解析为 struct；启动时打印非敏感配置。

### 7.2 迁移工具（`cmd/migrate/main.go`）

```bash
./migrate \
  --source sqlite:///path/to/old/db.sqlite3 \   # 旧 Django DB（存 base64 图片 + PDF path）
  --source-media /path/to/old/media \           # 旧 PDF 文件所在目录（pdfs/*.pdf）
  --target ./vol/se8.db \
  --target-media ./vol/media \
  --dry-run=false
```

**数据来源说明**：图片在旧项目中以 base64 存在 DB `Image.image` TextField，迁移工具直接从源 DB 解码写入文件系统；`--source-media` 只用于复制旧 `media/pdfs/*.pdf`。

**步骤**：

1. 连接源库（SQLite 与 Postgres URI 均支持）
2. 按 Book → Episode → Image 顺序遍历源 DB：
   - base64 解码 `Image.image`（源 DB 字段）→ 写 `vol/media/books/{book_id}/{ep_id}/{idx:03d}.{ext}`（魔数探测扩展名 `.jpg/.png/.webp`）
   - `Book.image`（源 DB 封面 base64）→ `vol/media/covers/{book_id}.jpg`
   - 复制 `--source-media/pdfs/*.pdf` → `vol/media/pdfs/`
3. 插入新库（批量事务，每 500 条 commit 一次）
4. 进度条：`schollz/progressbar`
5. `--dry-run` 只统计不写入

**容错**：

- 损坏 base64 跳过并记日志（不中断）
- 目标文件已存在默认跳过，`--overwrite` 覆盖
- 末尾打印 `{books, episodes, images_copied, images_skipped, pdfs_copied}` 汇总

### 7.3 构建

```make
.PHONY: build run migrate test lint tidy sqlc

build:
	go build -trimpath -ldflags="-s -w" -o bin/se8 ./cmd/se8
	go build -trimpath -ldflags="-s -w" -o bin/migrate ./cmd/migrate

build-linux-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o bin/se8-linux-arm64 ./cmd/se8

run: build
	./bin/se8

test:
	go test -race -cover ./...

lint:
	golangci-lint run

sqlc:
	sqlc generate

tidy:
	go mod tidy
```

**CGO_ENABLED=0**：纯 Go SQLite，单文件 static binary (~25MB)，任意平台交叉编译。

### 7.4 Dockerfile

```dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/se8 ./cmd/se8

FROM gcr.io/distroless/static-debian12
WORKDIR /app
COPY --from=builder /out/se8 /app/se8
ENV SE8_VOL_DIR=/app/vol
VOLUME ["/app/vol"]
EXPOSE 8000
ENTRYPOINT ["/app/se8"]
```

镜像 ~30MB（distroless + static）。

### 7.5 NAS 部署

```yaml
services:
  se8:
    image: se8:latest
    restart: unless-stopped
    ports:
      - "8000:8000"
    volumes:
      - ./vol:/app/vol
    environment:
      - SE8_ADDR=0.0.0.0:8000
      - SE8_WORKER_COUNT=2
```

首次启动日志打印 `[FIRST-RUN] admin password: XXXXX`，并写 `vol/first-run-password.txt`。

### 7.6 可观测性

- 日志：结构化 JSON（`log/slog`），按天轮转到 `vol/logs/app.log`（`lumberjack`）
- `/healthz`：返回 `{db: ok, vol: writable}`
- `/admin/stats` 页面：jobs by status / books count / images missing / storage bytes used
- 不做 Prometheus 指标（YAGNI）

---

## 8. 测试策略

### 8.1 测试金字塔

| 层级 | 框架 | 覆盖率目标 | 范围 |
|---|---|---|---|
| 单元 | `testing` + `testify/assert` | 80%+ | imaging、crawler 选择器、jobs 重试、auth 密码 |
| 集成 | `testing` + 临时 SQLite | 关键路径 | storage、jobs、迁移工具 |
| HTTP | `httptest.NewServer` + chi | 主要路由 | 认证、books/episodes API、HTMX 片段 |
| 端到端 | 本地脚本 | 烟雾测试 | 起二进制 → curl → 登录 → mock crawl |

### 8.2 关键测试点

**1. Crawler 选择器**（最易因上游改版坏掉）

- HTML fixture 放 `internal/crawler/testdata/`
- 断言数量与字段值
- 上游改版流程：改 fixture → 改解析代码

**2. Imaging**

- 3 张 200×300 纯色 PNG → 拼接 → 断言 600×200
- 长图 → PDF → 页数 = `ceil(total_height * scale / A4_height)`
- 损坏字节流跳过、不 panic

**3. Jobs 队列**

- `TestEnqueue_Dedup`：同 task_key 10 次 → 只 1 行
- `TestRunner_Recovery`：预插 status=running → 启动后 reset 为 pending
- `TestRunner_Retry`：handler 报错 → attempts 1→2→3 → 达上限置 failed
- `TestRunner_Concurrency`：10 handler 并行 → 同时运行 ≤ `maxWorkers`

**4. Auth**

- `TestFirstRun_CreateAdmin`：空库启动 → admin + password 文件
- `TestLogin_BcryptMismatch`：错密码 → 401
- `TestSession_Expiry`：过期 session → 302 `/login`

**5. 迁移工具**

- Fixture 旧 SQLite（5 books / 10 episodes / 20 images）
- 断言：新库条数一致、magic bytes 正确、文件 bytes > 0
- `--dry-run` 不写入

### 8.3 测试数据与隔离

- 每个测试独立 tempfile：`t.TempDir()` + `NewTestDB(t)`
- HTTP 测试：`newTestServer(t)` 返回 `*httptest.Server` + 自动登录的 cookie jar
- Mock 外部 HTTP：`httptest.NewServer` 伪造 se8.us

### 8.4 CI（GitHub Actions）

```yaml
- go test -race -coverprofile=coverage.out ./...
- go tool cover -func=coverage.out | tail -1   # < 70% 失败
- golangci-lint run
- go build ./...
```

### 8.5 未覆盖的部分（显式接受）

- 真实抓 se8.us 的 e2e 不跑（网络/内容敏感/需 referer），只用 fixture
- UI 视觉回归不做（HTMX 片段手工验收）
- 性能基准 benchmark 在 `internal/imaging/benchmark_test.go`，不进 CI 门槛

---

## 9. 附录：决策记录

### 9.1 头脑风暴阶段决策

| # | 问题 | 选择 | 理由 |
|---|---|---|---|
| 1 | 重写动机 | 部署简化（B） | 消除多进程运维负担 |
| 2 | Admin UI 方案 | 内置轻量 Web UI（A） | 保留点击浏览体验，无前端构建 |
| 3 | 后台任务方案 | 协程 + SQLite 任务表（B） | 崩溃安全 + 可观测 |
| 4 | 图片存储 | 文件系统（A） | 避免 SQLite 膨胀 |
| 5 | 旧数据处理 | 一次性迁移脚本（B） | 保留爬取成果 |
| 6 | 认证 | 简单账号密码 + Session（B） | NAS 部署需要，但复杂度受控 |
| 7 | 技术栈 | chi/sqlc/modernc.org/goquery/htmx | Go 社区主流、纯 Go、可交叉编译 |

### 9.2 被显式否决的方向

- **Clean Architecture 多层** — 对中等规模爬虫过度设计
- **扁平单包** — 文件膨胀后难维护
- **SPA 前端** — 需要额外构建链路，增加部署复杂度
- **无认证** — NAS 部署存在被局域网扫描风险
- **从零开始不迁移** — 会浪费历史爬取成果
- **Session + Captcha** — 单用户场景过度工程化
- **保留 PostgreSQL/Redis** — 与"部署简化"动机冲突

### 9.3 潜在后续改进（显式推后）

- 全文搜索：若 books 量极大可引入 SQLite FTS5（目前分页 + LIKE 足够）
- 多用户：当前单管理员；未来若需要可加 `role` 字段
- WebSocket 实时任务推送：HTMX 轮询对单用户足够，暂不引入 SSE/WebSocket
- Prometheus 指标：NAS 单机部署不需要；需要时再加 `/metrics`
- 镜像站点支持：通过 `SE8_BASE_URL` 已预留，但不做站点适配层

---

**下一步**：执行 `writing-plans` 技能，将本设计转化为分阶段实施计划。
