package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/unraid/apprise-go"

	"github.com/yufei/feixi/internal/config"
	"github.com/yufei/feixi/internal/cronex"
	"github.com/yufei/feixi/internal/daemon"
	"github.com/yufei/feixi/internal/notify"
	"github.com/yufei/feixi/internal/safenet"
	"github.com/yufei/feixi/internal/server"
	"github.com/yufei/feixi/internal/store"
)

func serve(ctx context.Context, log *slog.Logger) error {
	cfg := config.Default()
	if err := cfg.Validate(); err != nil {
		return err
	}
	log = setupLog(cfg)

	// Outbound notification traffic goes through the guarded transport, so it
	// is installed before any channel can be created.
	safenet.Install(safenet.Policy{
		AllowPrivate: cfg.AllowPrivateURLs,
		Connect:      cfg.NotifyConnectTimeout,
		Read:         cfg.NotifyReadTimeout,
	})

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	db.MaxChecks = cfg.MaxChecks
	db.PingLogLimit = cfg.PingLogLimit
	db.Sched = cronex.Scheduler{}

	project, err := db.Seed(ctx, cfg.SecretKey)
	if err != nil {
		return err
	}

	d := daemon.New(db, cfg, log)
	notifier := notify.NewWorker(db, cfg, log)
	d.Notifier = notifier
	d.Run(ctx)

	srv := server.New(cfg, db, d, apprise.SupportedSchemas(), notifier, notifier)
	httpSrv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv.Router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		<-ctx.Done()
		log.Info("正在关闭")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		errCh <- httpSrv.Shutdown(shutdownCtx)
	}()

	log.Info("监听中", "addr", cfg.Listen, "site_root", cfg.SiteRoot,
		"project", project.Name, "db", cfg.DBPath)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return <-errCh
}

func setupLog(cfg *config.Config) *slog.Logger {
	level := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})))
	return slog.Default()
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
