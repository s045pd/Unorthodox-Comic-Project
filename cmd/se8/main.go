package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/s045pd/se8/internal/auth"
	"github.com/s045pd/se8/internal/config"
	"github.com/s045pd/se8/internal/crawler"
	"github.com/s045pd/se8/internal/jobs"
	"github.com/s045pd/se8/internal/scheduler"
	"github.com/s045pd/se8/internal/storage"
	"github.com/s045pd/se8/internal/web"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(".env")
	if err != nil {
		return err
	}

	logger := newLogger(cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(logger)

	volDir, err := filepath.Abs(cfg.VolDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(volDir, "media"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(volDir, "logs"), 0o755); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := storage.Open(ctx, filepath.Join(volDir, "se8.db"))
	if err != nil {
		return err
	}
	defer db.Close()

	authStore := auth.NewStore(db)
	pw, created, err := authStore.EnsureFirstRunAdmin(ctx, volDir)
	if err != nil {
		return err
	}
	if created {
		logger.Info("first-run admin created", "username", "admin", "password_file",
			filepath.Join(volDir, "first-run-password.txt"))
		fmt.Fprintf(os.Stdout, "\n[FIRST-RUN] admin password: %s\n", pw)
	}

	client := crawler.NewClient(cfg.BaseURL, cfg.HTTPTimeout)
	extractor := crawler.NewExtractor(client)

	queue := jobs.NewQueue(db)
	runner := jobs.NewRunner(queue, cfg.WorkerCount)
	deps := &jobs.Deps{
		DB: db, Queue: queue, Extractor: extractor, Client: client,
		VolDir: volDir, MaxPage: cfg.MaxPage,
	}
	jobs.Register(runner, deps)
	go runner.Run(ctx)

	sched := scheduler.New(queue)
	if err := sched.Start(ctx); err != nil {
		return fmt.Errorf("scheduler start: %w", err)
	}

	// Periodic session cleanup
	go cleanSessionsLoop(ctx, authStore)

	tpl, err := web.LoadTemplates()
	if err != nil {
		return err
	}
	srv := &web.Server{
		DB: db, Auth: authStore, Queue: queue, VolDir: volDir,
		Templates: tpl, Logger: logger,
	}
	httpSrv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      srv.Router(),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 5 * time.Minute,
		IdleTimeout:  120 * time.Second,
	}
	logger.Info("serving", "addr", cfg.Addr)
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http serve", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}

func newLogger(level, format string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	if format == "json" {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}

func cleanSessionsLoop(ctx context.Context, store *auth.Store) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = store.DeleteExpiredSessions(ctx)
		}
	}
}
