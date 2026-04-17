package scheduler

import (
	"context"
	"log/slog"

	"github.com/robfig/cron/v3"

	"github.com/s045pd/se8/internal/jobs"
)

type Scheduler struct {
	cron  *cron.Cron
	queue *jobs.Queue
}

func New(q *jobs.Queue) *Scheduler {
	return &Scheduler{
		cron:  cron.New(cron.WithLocation(timeLocation())),
		queue: q,
	}
}

// Start registers built-in schedules and begins ticking. Stop with ctx cancel.
func (s *Scheduler) Start(ctx context.Context) error {
	specs := []struct {
		schedule string
		kind     jobs.Kind
		key      string
	}{
		{"0 0 * * *", jobs.KindFindBooks, "find_books:daily"},
		{"0 1 * * *", jobs.KindFixImages, "fix_images:daily"},
		{"0 2 * * *", jobs.KindFixPDF, "fix_pdf:daily"},
	}
	for _, sp := range specs {
		kind, key := sp.kind, sp.key
		_, err := s.cron.AddFunc(sp.schedule, func() {
			if err := s.queue.Enqueue(ctx, kind, key, nil); err != nil {
				slog.Error("scheduler enqueue", "kind", kind, "err", err)
			}
		})
		if err != nil {
			return err
		}
	}
	s.cron.Start()
	go func() {
		<-ctx.Done()
		<-s.cron.Stop().Done()
	}()
	return nil
}
