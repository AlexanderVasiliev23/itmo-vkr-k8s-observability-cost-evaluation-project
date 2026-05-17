package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/fx"

	"obs-bench/internal/app"
)

func main() {
	runCtx, stopRun := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopRun()

	fxApp := fx.New(
		fx.NopLogger,
		app.Module(runCtx),
	)

	exitCode := 0
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := fxApp.Stop(stopCtx); err != nil {
			slog.Error("fx stop", "err", err)
		}
		if exitCode != 0 {
			os.Exit(exitCode)
		}
	}()

	if err := fxApp.Start(runCtx); err != nil {
		slog.Error(err.Error())
		exitCode = 1
	}
}
