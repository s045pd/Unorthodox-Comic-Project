package jobs

import (
	"context"
	"encoding/json"
	"time"
)

type Kind string

const (
	KindFindBooks     Kind = "find_books"
	KindFindEpisodes  Kind = "find_episodes"
	KindFindImages    Kind = "find_images"
	KindDownloadImage Kind = "download_image"
	KindConvertPDF    Kind = "convert_pdf"
	KindFixImages     Kind = "fix_images"
	KindFixPDF        Kind = "fix_pdf"
)

type Status string

const (
	StatusPending Status = "pending"
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
)

// Handler executes one job. Return nil on success; non-nil triggers retry.
type Handler func(ctx context.Context, payload json.RawMessage) error

type Job struct {
	ID          int64
	Kind        Kind
	Payload     json.RawMessage
	TaskKey     string
	Status      Status
	Priority    int
	Attempts    int
	MaxAttempts int
	LastError   string
	RunAt       time.Time
	StartedAt   *time.Time
	FinishedAt  *time.Time
	CreatedAt   time.Time
}

type EnqueueOption func(*enqueueOpts)

type enqueueOpts struct {
	priority    int
	maxAttempts int
	runAt       time.Time
}

func WithPriority(p int) EnqueueOption {
	return func(o *enqueueOpts) { o.priority = p }
}

func WithMaxAttempts(n int) EnqueueOption {
	return func(o *enqueueOpts) { o.maxAttempts = n }
}

func WithRunAt(t time.Time) EnqueueOption {
	return func(o *enqueueOpts) { o.runAt = t }
}

func WithDelay(d time.Duration) EnqueueOption {
	return func(o *enqueueOpts) { o.runAt = time.Now().Add(d) }
}
