package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/yufei/feixi/internal/config"
	"github.com/yufei/feixi/internal/cronex"
	"github.com/yufei/feixi/internal/engine"
	"github.com/yufei/feixi/internal/store"
)

func init() {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestVersionIsStamped(t *testing.T) {
	if version == "" {
		t.Error("version should default to dev when not injected by CI")
	}
}

func TestSetupLogLevels(t *testing.T) {
	tests := []struct {
		level string
		want  map[slog.Level]bool
	}{
		{"debug", map[slog.Level]bool{slog.LevelDebug: true, slog.LevelInfo: true}},
		{"", map[slog.Level]bool{slog.LevelDebug: false, slog.LevelInfo: true}},
		{"warn", map[slog.Level]bool{slog.LevelInfo: false, slog.LevelWarn: true}},
		{"error", map[slog.Level]bool{slog.LevelWarn: false, slog.LevelError: true}},
		// 认不出的级别不至于把日志关掉。
		{"nonsense", map[slog.Level]bool{slog.LevelInfo: true}},
	}
	for _, tt := range tests {
		cfg := config.Default()
		cfg.LogLevel = tt.level
		log := setupLog(cfg)
		for level, want := range tt.want {
			if got := log.Enabled(context.Background(), level); got != want {
				t.Errorf("LogLevel=%q enabled(%v) = %v, want %v", tt.level, level, got, want)
			}
		}
	}
}

func TestSignalContextStopsOnSIGTERM(t *testing.T) {
	ctx, cancel := signalContext()
	defer cancel()

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("SIGTERM did not cancel the context")
	}
}

func TestRunPruneTrimsTheConfiguredDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prune.db")
	t.Setenv("FEIXI_DB", path)

	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	db.Sched = cronex.Scheduler{}
	db.MaxChecks = 100
	db.PingLogLimit = 100
	if _, err := db.Seed(context.Background(), "secret"); err != nil {
		t.Fatal(err)
	}
	project, err := db.Project(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c, err := db.CreateCheck(context.Background(), &store.NewCheck{
		ProjectID: project.ID, Name: "任务", Kind: engine.KindSimple,
		Timeout: engine.DefaultTimeout, Grace: engine.DefaultGrace, TZ: "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, _, _, _, err := db.RecordPing(context.Background(), c.ID, engine.ActionSuccess, store.PingInput{}); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	if err := runPrune(context.Background(), slog.Default()); err != nil {
		t.Fatalf("runPrune: %v", err)
	}

	// 默认保留 100 条，这里只验证它能连库并跑完；换成小上限就该真剪。
	t.Setenv("FEIXI_PING_LOG_LIMIT", "2")
	if err := runPrune(context.Background(), slog.Default()); err != nil {
		t.Fatalf("runPrune with a limit: %v", err)
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	rows, err := reopened.ListPings(context.Background(), c.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Errorf("kept %d pings, want the last 2", len(rows))
	}
}

func TestRunPruneReportsABrokenDatabase(t *testing.T) {
	// /dev/null 下面建不出目录，连库这一步就该把错误抛出来。
	t.Setenv("FEIXI_DB", filepath.Join(os.DevNull, "feixi.db"))
	if err := runPrune(context.Background(), slog.Default()); err == nil {
		t.Fatal("expected an open failure")
	}
}

func TestServeValidatesTheSiteRoot(t *testing.T) {
	t.Setenv("FEIXI_SITE_ROOT", "status.example.com")
	t.Setenv("FEIXI_DB", filepath.Join(t.TempDir(), "serve.db"))

	err := serve(context.Background())
	if err == nil {
		t.Fatal("an invalid site root should stop startup")
	}
	if !strings.Contains(err.Error(), "FEIXI_SITE_ROOT") {
		t.Errorf("err = %v, want it to name the variable", err)
	}
}

func TestServeReportsAClosedDatabasePath(t *testing.T) {
	t.Setenv("FEIXI_SITE_ROOT", "http://example.test")
	t.Setenv("FEIXI_DB", filepath.Join(os.DevNull, "feixi.db"))

	if err := serve(context.Background()); err == nil {
		t.Fatal("expected the database open to fail")
	}
}

func TestServeServesThenShutsDown(t *testing.T) {
	addr := freeAddr(t)
	t.Setenv("FEIXI_LISTEN", addr)
	t.Setenv("FEIXI_SITE_ROOT", "http://"+addr)
	t.Setenv("FEIXI_DB", filepath.Join(t.TempDir(), "serve.db"))
	t.Setenv("FEIXI_SECRET_KEY", "test-secret")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- serve(ctx) }()

	waitFor(t, func() bool {
		resp, err := http.Get("http://" + addr + "/api/v1/status/")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode == http.StatusOK
	}, "the server should answer /api/v1/status/ once it is up")

	// 前端没构建时也只该是 503，而不是 500。
	if resp, err := http.Get("http://" + addr + "/"); err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("GET / = %d", resp.StatusCode)
		}
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("serve returned %v, want a clean shutdown", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not return after the context was cancelled")
	}
}

func TestServeReportsAPortInTheUse(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	t.Setenv("FEIXI_LISTEN", listener.Addr().String())
	t.Setenv("FEIXI_SITE_ROOT", "http://example.test")
	t.Setenv("FEIXI_DB", filepath.Join(t.TempDir(), "serve.db"))

	errCh := make(chan error, 1)
	go func() { errCh <- serve(context.Background()) }()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("listening on a taken port should fail")
		}
		if !strings.Contains(err.Error(), "address already in use") {
			t.Errorf("err = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not report the bind failure")
	}
}

// freeAddr 占一个空闲端口再放开，用来喂给 FEIXI_LISTEN。
func freeAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	return addr
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", msg)
}
