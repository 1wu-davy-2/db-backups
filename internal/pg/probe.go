package pg

import (
	"context"
	"database/sql"

	"fmt"
	"github.com/jackc/pgx/v5/stdlib"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type ConnInfo struct {
	Host     string
	Port     int
	DBName   string
	User     string
	Password string
	SSLMode  string
}

var allowedSSL = map[string]bool{
	"disable": true, "allow": true, "prefer": true,
	"require": true, "verify-ca": true, "verify-full": true,
}

func (c ConnInfo) dsn() (string, error) {
	ssl := c.SSLMode
	if ssl == "" {
		ssl = "prefer"
	}
	if !allowedSSL[ssl] {
		return "", fmt.Errorf("不支持的 sslmode: %s", ssl)
	}
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.User, c.Password),
		Host:   net.JoinHostPort(c.Host, strconv.Itoa(c.Port)),
		Path:   "/" + c.DBName,
	}
	q := u.Query()
	q.Set("sslmode", ssl)
	q.Set("connect_timeout", "10")
	q.Set("application_name", "dbbackups")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (c ConnInfo) Open() (*sql.DB, error) {
	dsn, err := c.dsn()
	if err != nil {
		return nil, err
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	db := stdlib.OpenDB(*cfg)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)
	return db, nil
}

// Redacted 用于日志与错误信息，避免密码外泄。
func (c ConnInfo) Redacted() string {
	return fmt.Sprintf("%s@%s:%d/%s", c.User, c.Host, c.Port, c.DBName)
}

type ServerInfo struct {
	Version       string
	VersionNum    int
	Major         int
	DBSizeBytes   int64
	UnreadableTbl int
	TableCount    int
}

// Probe 连接目标库，取版本、容量与只读权限情况。
func Probe(ctx context.Context, ci ConnInfo) (*ServerInfo, error) {
	dsn, err := ci.dsn()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("连接失败: %w", redactErr(err, ci.Password))
	}
	defer conn.Close(context.Background())

	var info ServerInfo
	if err := conn.QueryRow(ctx, `
		SELECT current_setting('server_version'),
		       current_setting('server_version_num')::int,
		       pg_database_size(current_database())`).
		Scan(&info.Version, &info.VersionNum, &info.DBSizeBytes); err != nil {
		return nil, fmt.Errorf("读取版本失败: %w", redactErr(err, ci.Password))
	}
	info.Major = info.VersionNum / 10000

	// 顺带校验备份账号是否真的只读：统计当前用户无权 SELECT 的表。
	_ = conn.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE NOT has_table_privilege(c.oid, 'SELECT'))
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind IN ('r','p')
		  AND n.nspname NOT IN ('pg_catalog','information_schema')
		  AND n.nspname NOT LIKE 'pg_toast%'`).
		Scan(&info.TableCount, &info.UnreadableTbl)
	return &info, nil
}

func redactErr(err error, secret string) error {
	if err == nil || secret == "" {
		return err
	}
	msg := strings.ReplaceAll(err.Error(), secret, "******")
	return fmt.Errorf("%s", msg)
}
