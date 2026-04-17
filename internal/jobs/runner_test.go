package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunner_Recovery_ResetsRunning(t *testing.T) {
	q, db := newTestQueue(t)
	ctx := context.Background()

	// Insert a "stuck" running job
	_, err := db.ExecContext(ctx, `
		INSERT INTO jobs (kind, payload, task_key, status, priority, max_attempts, run_at, created_at, started_at)
		VALUES ('find_books', '{}', 'stuck', 'running', 0, 3, ?, ?, ?)`,
		time.Now().Unix(), time.Now().Unix(), time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}

	r := NewRunner(q, 1)
	if err := r.RecoverStuckJobs(ctx); err != nil {
		t.Fatal(err)
	}

	var status string
	db.QueryRowContext(ctx, `SELECT status FROM jobs WHERE task_key='stuck'`).Scan(&status)
	if status != "pending" {
		t.Errorf("status = %q, want pending", status)
	}
}

func TestRunner_Retry_OnError(t *testing.T) {
	q, db := newTestQueue(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls atomic.Int32
	r := NewRunner(q, 1)
	r.Register(KindFindBooks, func(ctx context.Context, p json.RawMessage) error {
		calls.Add(1)
		return errors.New("simulated failure")
	})
	r.pollInterval = 50 * time.Millisecond
	r.backoffBase = 20 * time.Millisecond

	_ = q.Enqueue(ctx, KindFindBooks, "t:retry", nil, WithMaxAttempts(3))

	go r.Run(ctx)

	// Wait for 3 attempts
	deadline := time.Now().Add(3 * time.Second)
	for calls.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	time.Sleep(100 * time.Millisecond)

	if got := calls.Load(); got != 3 {
		t.Errorf("calls = %d, want 3", got)
	}

	var status string
	var attempts int
	db.QueryRowContext(context.Background(),
		`SELECT status, attempts FROM jobs WHERE task_key='t:retry'`).Scan(&status, &attempts)
	if status != "failed" {
		t.Errorf("status = %q, want failed", status)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

func TestRunner_Success(t *testing.T) {
	q, db := newTestQueue(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls atomic.Int32
	r := NewRunner(q, 1)
	r.Register(KindFindBooks, func(ctx context.Context, p json.RawMessage) error {
		calls.Add(1)
		return nil
	})
	r.pollInterval = 50 * time.Millisecond

	_ = q.Enqueue(ctx, KindFindBooks, "t:success", nil)

	go r.Run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	time.Sleep(100 * time.Millisecond)

	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1", calls.Load())
	}

	var status string
	db.QueryRowContext(context.Background(),
		`SELECT status FROM jobs WHERE task_key='t:success'`).Scan(&status)
	if status != "done" {
		t.Errorf("status = %q, want done", status)
	}
}

func TestRunner_Concurrency(t *testing.T) {
	q, _ := newTestQueue(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var running atomic.Int32
	var maxRunning atomic.Int32
	var mu sync.Mutex

	r := NewRunner(q, 3) // max 3 workers
	r.Register(KindFindBooks, func(ctx context.Context, p json.RawMessage) error {
		n := running.Add(1)
		mu.Lock()
		if n > maxRunning.Load() {
			maxRunning.Store(n)
		}
		mu.Unlock()
		time.Sleep(100 * time.Millisecond)
		running.Add(-1)
		return nil
	})
	r.pollInterval = 20 * time.Millisecond

	for i := 0; i < 10; i++ {
		_ = q.Enqueue(ctx, KindFindBooks, "t:conc-"+string(rune('a'+i)), nil)
	}

	go r.Run(ctx)

	time.Sleep(800 * time.Millisecond)
	cancel()

	if got := maxRunning.Load(); got > 3 {
		t.Errorf("maxRunning = %d, exceeded limit of 3", got)
	}
	if got := maxRunning.Load(); got < 2 {
		t.Errorf("maxRunning = %d, expected >=2 to prove parallelism", got)
	}
}
