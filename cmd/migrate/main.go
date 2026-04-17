package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

type flags struct {
	source      string
	sourceMedia string
	target      string
	targetMedia string
	dryRun      bool
	overwrite   bool
}

func main() {
	var f flags
	flag.StringVar(&f.source, "source", "", "source DB URI (sqlite:///path or postgres://...)")
	flag.StringVar(&f.sourceMedia, "source-media", "", "source media directory (for PDFs)")
	flag.StringVar(&f.target, "target", "./vol/se8.db", "target SQLite path")
	flag.StringVar(&f.targetMedia, "target-media", "./vol/media", "target media directory")
	flag.BoolVar(&f.dryRun, "dry-run", false, "don't write anything, just count")
	flag.BoolVar(&f.overwrite, "overwrite", false, "overwrite existing target files")
	flag.Parse()

	if f.source == "" {
		fmt.Fprintln(os.Stderr, "--source is required")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := migrate(ctx, f); err != nil {
		fmt.Fprintln(os.Stderr, "migrate failed:", err)
		os.Exit(1)
	}
}
