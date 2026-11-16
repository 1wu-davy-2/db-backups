package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/weigong/dbbackups/internal/api"
	"github.com/weigong/dbbackups/internal/backup"
	"github.com/weigong/dbbackups/internal/config"
	"github.com/weigong/dbbackups/internal/pg"
	"github.com/weigong/dbbackups/internal/secrets"
	"github.com/weigong/dbbackups/internal/storage"
	"github.com/weigong/dbbackups/internal/store"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("配置错误", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := store.Migrate(ctx, cfg.DatabaseURL); err != nil {
		slog.Error("数据库迁移失败", "err", err)
		os.Exit(1)
	}
	pool, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("连接元数据库失败", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	st := store.New(pool)

	keys, err := secrets.LoadKeyring(os.Getenv("BACKUP_MASTER_KEY"), cfg.MasterKeyFile)
	if err != nil {
		slog.Error("加载主密钥失败", "err", err)
		os.Exit(1)
	}

	tools := pg.NewToolchain(cfg.PgBinDirs)
	if list := tools.List(); len(list) == 0 {
		slog.Warn("未发现 pg_dump，备份将无法执行；请安装 postgresql-client 或设置 BACKUP_PG_BIN_DIRS")
	} else {
		for _, t := range list {
			slog.Info("发现 pg 客户端", "major", t["major"], "version", t["version"])
		}
	}

	blob, err := storage.NewLocal(filepath.Join(cfg.DataDir, "objects"))
	if err != nil {
		slog.Error("初始化存储失败", "err", err)
		os.Exit(1)
	}

	logs := backup.NewLogHub()
	runner := backup.NewRunner(st, keys, tools, blob, logs, cfg.DataDir, cfg.MaxConcurrent)
	sched := backup.NewScheduler(st, runner, func(msg string) {
		slog.Info("scheduler", "msg", msg)
	})
	if err := sched.Start(ctx); err != nil {
		slog.Error("启动调度器失败", "err", err)
		os.Exit(1)
	}
	defer sched.Stop()

	if n, err := st.FailStaleJobs(ctx); err == nil && n > 0 {
		slog.Warn("已把上次异常退出时残留的任务标记为失败", "count", n)
	}

	webDir := os.Getenv("BACKUP_WEB_DIR")
	if webDir == "" {
		webDir = "web/dist"
	}

	srv := &api.Server{
		Store: st, Keys: keys, Tools: tools, Runner: runner, Sched: sched, Logs: logs,
		Blob: blob, DataDir: cfg.DataDir, WebDir: webDir,
		SecureCookie: os.Getenv("BACKUP_COOKIE_SECURE") == "1",
		SessionTTL:   time.Duration(cfg.SessionTTLDay) * 24 * time.Hour,
	}

	go janitor(ctx, st)

	httpSrv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		slog.Info("服务已启动", "addr", cfg.HTTPAddr, "version", api.Version)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP 服务异常", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("正在关闭...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
}

func janitor(ctx context.Context, st *store.Store) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := st.PurgeExpiredSessions(ctx); err == nil && n > 0 {
				slog.Debug("清理过期会话", "count", n)
			}
		}
	}
}
