package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"time"

	"golang.org/x/sync/semaphore"
)

type Runner struct {
	queue        *Queue
	maxWorkers   int
	sem          *semaphore.Weighted
	handlers     map[Kind]Handler
	pollInterval time.Duration
	backoffBase  time.Duration
	logger       *slog.Logger
}

func NewRunner(q *Queue, maxWorkers int) *Runner {
	if maxWorkers < 1 {
		maxWorkers = 1
	}
	return &Runner{
		queue:        q,
		maxWorkers:   maxWorkers,
		sem:          semaphore.NewWeighted(int64(maxWorkers)),
		handlers:     make(map[Kind]Handler),
		pollInterval: 2 * time.Second,
		backoffBase:  10 * time.Second,
		logger:       slog.Default(),
	}
}

func (r *Runner) Register(kind Kind, h Handler) {
	r.handlers[kind] = h
}

// RecoverStuckJobs flips any status=running rows back to pending.
// Call once at startup before Run.
func (r *Runner) RecoverStuckJobs(ctx context.Context) error {
	_, err := r.queue.db.ExecContext(ctx,
		`UPDATE jobs SET status='pending', started_at=NULL WHERE status='running'`)
	return err
}

func (r *Runner) Run(ctx context.Context) {
	_ = r.RecoverStuckJobs(ctx)

	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.drain(ctx)
		}
	}
}

func (r *Runner) drain(ctx context.Context) {
	rows, err := r.queue.db.QueryContext(ctx, `
		SELECT id, kind, payload, task_key, attempts, max_attempts
		FROM jobs
		WHERE status='pending' AND run_at <= ?
		ORDER BY priority DESC, id ASC
		LIMIT ?`, time.Now().Unix(), r.maxWorkers*4)
	if err != nil {
		r.logger.Error("drain query", "err", err)
		return
	}
	type row struct {
		id          int64
		kind        string
		payload     string
		taskKey     string
		attempts    int
		maxAttempts int
	}
	var jobs []row
	for rows.Next() {
		var j row
		if err := rows.Scan(&j.id, &j.kind, &j.payload, &j.taskKey, &j.attempts, &j.maxAttempts); err != nil {
			r.logger.Error("drain scan", "err", err)
			continue
		}
		jobs = append(jobs, j)
	}
	rows.Close()

	for _, j := range jobs {
		if err := r.sem.Acquire(ctx, 1); err != nil {
			return
		}
		go func(j row) {
			defer r.sem.Release(1)
			r.execute(ctx, j.id, Kind(j.kind), json.RawMessage(j.payload), j.taskKey, j.attempts, j.maxAttempts)
		}(j)
	}
}

func (r *Runner) execute(ctx context.Context, id int64, kind Kind, payload json.RawMessage, taskKey string, attempts, maxAttempts int) {
	h, ok := r.handlers[kind]
	if !ok {
		r.failJob(ctx, id, fmt.Errorf("no handler for kind %q", kind))
		return
	}

	// Claim the job (atomic pending → running)
	res, err := r.queue.db.ExecContext(ctx,
		`UPDATE jobs SET status='running', started_at=? WHERE id=? AND status='pending'`,
		time.Now().Unix(), id)
	if err != nil {
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return // someone else grabbed it
	}

	r.logger.Info("job start", "id", id, "kind", kind, "task_key", taskKey, "attempt", attempts+1)
	err = h(ctx, payload)

	if err == nil {
		_, _ = r.queue.db.ExecContext(ctx,
			`UPDATE jobs SET status='done', finished_at=?, last_error='' WHERE id=?`,
			time.Now().Unix(), id)
		r.logger.Info("job done", "id", id, "kind", kind)
		return
	}

	newAttempts := attempts + 1
	if newAttempts >= maxAttempts {
		_, _ = r.queue.db.ExecContext(ctx,
			`UPDATE jobs SET status='failed', finished_at=?, last_error=?, attempts=? WHERE id=?`,
			time.Now().Unix(), err.Error(), newAttempts, id)
		r.logger.Warn("job failed", "id", id, "kind", kind, "err", err, "attempts", newAttempts)
		return
	}

	// Exponential backoff with jitter: base * 2^(attempt-1), capped at 5 min
	delay := time.Duration(math.Pow(2, float64(newAttempts-1))) * r.backoffBase
	if delay > 5*time.Minute {
		delay = 5 * time.Minute
	}
	runAt := time.Now().Add(delay).Unix()
	_, _ = r.queue.db.ExecContext(ctx,
		`UPDATE jobs SET status='pending', run_at=?, last_error=?, attempts=? WHERE id=?`,
		runAt, err.Error(), newAttempts, id)
	r.logger.Warn("job retry scheduled", "id", id, "kind", kind, "err", err, "delay", delay)
}

func (r *Runner) failJob(ctx context.Context, id int64, err error) {
	_, _ = r.queue.db.ExecContext(ctx,
		`UPDATE jobs SET status='failed', finished_at=?, last_error=? WHERE id=?`,
		time.Now().Unix(), err.Error(), id)
}
