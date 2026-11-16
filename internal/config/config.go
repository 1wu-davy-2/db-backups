package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config 全部来自环境变量，无配置文件，便于容器化。
type Config struct {
	HTTPAddr      string
	DatabaseURL   string
	DataDir       string
	MasterKeyFile string
	PgBinDirs     []string
	SessionTTLDay int
	MaxConcurrent int
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func Load() (*Config, error) {
	dataDir := env("BACKUP_DATA_DIR", "./data")
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	ttl, err := strconv.Atoi(env("BACKUP_SESSION_TTL_DAYS", "7"))
	if err != nil || ttl <= 0 {
		ttl = 7
	}
	conc, err := strconv.Atoi(env("BACKUP_MAX_CONCURRENT", "4"))
	if err != nil || conc <= 0 {
		conc = 4
	}

	dirs := []string{
		env("BACKUP_PG_BIN_DIRS", ""),
		"/usr/lib/postgresql/19/bin", "/usr/lib/postgresql/18/bin",
		"/usr/lib/postgresql/17/bin", "/usr/lib/postgresql/16/bin",
		"/usr/lib/postgresql/15/bin", "/usr/lib/postgresql/14/bin",
		"/usr/lib/postgresql/13/bin",
		"/usr/pgsql-19/bin", "/usr/pgsql-18/bin", "/usr/pgsql-17/bin",
		"/usr/pgsql-16/bin", "/usr/pgsql-15/bin",
	}
	var pgDirs []string
	for _, d := range strings.Split(strings.Join(dirs, ","), ",") {
		if d = strings.TrimSpace(d); d != "" {
			pgDirs = append(pgDirs, d)
		}
	}

	cfg := &Config{
		HTTPAddr:      env("BACKUP_HTTP_ADDR", ":8080"),
		DatabaseURL:   env("BACKUP_DATABASE_URL", ""),
		DataDir:       dataDir,
		MasterKeyFile: filepath.Join(dataDir, "master.key"),
		PgBinDirs:     pgDirs,
		SessionTTLDay: ttl,
		MaxConcurrent: conc,
	}
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("BACKUP_DATABASE_URL is required, e.g. postgres://user:pass@127.0.0.1:5432/dbbackups?sslmode=disable")
	}
	return cfg, nil
}
