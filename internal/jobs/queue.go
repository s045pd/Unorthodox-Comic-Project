package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type Queue struct {
	db *sql.DB
}

func NewQueue(db *sql.DB) *Queue {
	return &Queue{db: db}
}

func (q *Queue) Enqueue(ctx context.Context, kind Kind, taskKey string, payload any, opts ...EnqueueOption) error {
	o := enqueueOpts{priority: 0, maxAttempts: 3, runAt: time.Now()}
	for _, opt := range opts {
		opt(&o)
	}

	var payloadJSON []byte
	if payload == nil {
		payloadJSON = []byte("{}")
	} else {
		var err error
		payloadJSON, err = json.Marshal(payload)
		if err != nil {
			return err
		}
	}

	// Clear any completed/failed row with this key so the new enqueue can
	// proceed. Pending/running rows are preserved — INSERT OR IGNORE keeps
	// them deduplicated.
	if _, err := q.db.ExecContext(ctx,
		`DELETE FROM jobs WHERE task_key=? AND status IN ('done','failed')`,
		taskKey); err != nil {
		return err
	}

	_, err := q.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO jobs (kind, payload, task_key, status, priority, max_attempts, run_at, created_at)
		VALUES (?, ?, ?, 'pending', ?, ?, ?, ?)`,
		string(kind), string(payloadJSON), taskKey,
		o.priority, o.maxAttempts, o.runAt.Unix(), time.Now().Unix())
	return err
}
