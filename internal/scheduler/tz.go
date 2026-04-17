package scheduler

import (
	"log/slog"
	"time"
)

func timeLocation() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		slog.Warn("LoadLocation failed, using Local", "err", err)
		return time.Local
	}
	return loc
}
