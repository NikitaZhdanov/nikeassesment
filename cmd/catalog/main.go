package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/nikitazhdanov/nikeassesment/internal/app"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	config, err := app.LoadConfig(os.Getenv)
	if err == nil {
		err = app.Run(ctx, config, log)
	}
	if err != nil {
		log.Error("catalog stopped", "error", err)
		os.Exit(1)
	}
}
