package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	MySQLBinDirs  []string
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

	cfg := &Config{
		HTTPAddr:      env("BACKUP_HTTP_ADDR", ":8080"),
		DatabaseURL:   env("BACKUP_DATABASE_URL", ""),
		DataDir:       dataDir,
		MasterKeyFile: filepath.Join(dataDir, "master.key"),
		PgBinDirs:     PgBinDirs(dataDir, os.Getenv("BACKUP_PG_BIN_DIRS")),
		MySQLBinDirs:  MySQLBinDirs(dataDir, os.Getenv("BACKUP_MYSQL_BIN_DIRS")),
		SessionTTLDay: ttl,
		MaxConcurrent: conc,
	}
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("BACKUP_DATABASE_URL is required, e.g. postgres://user:pass@127.0.0.1:5432/dbbackups?sslmode=disable")
	}
	return cfg, nil
}

func MySQLBinDirs(dataDir, extra string) []string {
	var out []string
	for _, dir := range strings.Split(extra, ",") {
		if dir = strings.TrimSpace(dir); dir != "" {
			out = append(out, dir)
		}
	}
	patterns := []string{"/usr/bin", "/usr/local/mysql/bin", "/opt/mysql/*/bin", "/opt/mariadb/*/bin", "/opt/homebrew/opt/mysql*/bin", "/opt/homebrew/opt/mariadb*/bin"}
	if runtime.GOOS == "windows" {
		patterns = []string{`C:\Program Files\MySQL\*\bin`, `C:\Program Files\MariaDB *\bin`}
	}
	patterns = append(patterns, filepath.Join(dataDir, "mysqlclient", "*", "bin"))
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		out = append(out, matches...)
	}
	return out
}

// PgBinDirs 汇总所有 pg_dump 候选目录。
// 多版本共存是刻意设计：客户端主版本必须 >= 服务端主版本，固定单一版本无法覆盖所有目标库。
func PgBinDirs(dataDir, extra string) []string {
	var out []string
	// 用户显式配置的路径始终保留，跨平台也不过滤。
	for _, d := range strings.Split(extra, ",") {
		if d = strings.TrimSpace(d); d != "" {
			out = append(out, d)
		}
	}

	var patterns []string
	if runtime.GOOS == "windows" {
		patterns = []string{
			`C:\Program Files\PostgreSQL\*\bin`,
			`C:\Program Files (x86)\PostgreSQL\*\bin`,
		}
	} else {
		patterns = []string{
			"/usr/lib/postgresql/19/bin", "/usr/lib/postgresql/18/bin",
			"/usr/lib/postgresql/17/bin", "/usr/lib/postgresql/16/bin",
			"/usr/lib/postgresql/15/bin", "/usr/lib/postgresql/14/bin",
			"/usr/lib/postgresql/13/bin",
			"/usr/pgsql-19/bin", "/usr/pgsql-18/bin", "/usr/pgsql-17/bin",
			"/usr/pgsql-16/bin", "/usr/pgsql-15/bin",
			"/opt/homebrew/opt/postgresql@*/bin",
			"/usr/local/opt/postgresql@*/bin",
			"/Applications/Postgres.app/Contents/Versions/*/bin",
		}
	}
	patterns = append(patterns, filepath.Join(dataDir, "pgclient", "*", "bin"))

	for _, pat := range patterns {
		matches, err := filepath.Glob(pat)
		if err != nil {
			continue
		}
		out = append(out, matches...)
	}
	return out
}
