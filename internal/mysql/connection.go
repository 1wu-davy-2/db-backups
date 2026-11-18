package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"github.com/weigong/dbbackups/internal/domain"
	"github.com/weigong/dbbackups/internal/pg"
)

func Open(target *domain.Target, password string) (*sql.DB, error) {
	cfg := driver.NewConfig()
	cfg.User = target.Username
	cfg.Passwd = password
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(target.Host, strconv.Itoa(target.Port))
	cfg.DBName = target.DBName
	cfg.Timeout = 10 * time.Second
	cfg.ReadTimeout = 30 * time.Second
	cfg.WriteTimeout = 10 * time.Second
	cfg.MaxAllowedPacket = 4 << 20
	_ = cfg.Apply(driver.Charset("utf8mb4", ""))
	switch target.SSLMode {
	case "disable":
		cfg.TLSConfig = "false"
	case "require", "verify-full":
		cfg.TLSConfig = "true"
	default:
		return nil, fmt.Errorf("MySQL/MariaDB TLS 仅支持 disable 或 verify-full（校验证书）")
	}
	connector, err := driver.NewConnector(cfg)
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)
	return db, nil
}

func Probe(ctx context.Context, target *domain.Target, password string) (*pg.ServerInfo, error) {
	db, err := Open(target, password)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var version string
	if err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		return nil, redacted(err, password)
	}
	parsed, err := ParseVersion(version)
	if err != nil {
		return nil, err
	}
	if parsed.Engine != target.Engine {
		return nil, fmt.Errorf("服务端是 %s，请选择对应的数据库类型", parsed.Engine)
	}
	info := &pg.ServerInfo{Version: version, Major: parsed.Major, VersionNum: parsed.Major*10000 + parsed.Minor*100}
	err = db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(data_length + index_length),0) FROM information_schema.tables WHERE table_schema = ?`, target.DBName).Scan(&info.TableCount, &info.DBSizeBytes)
	if err != nil {
		return nil, redacted(err, password)
	}
	return info, nil
}

func CheckTransactionalTables(ctx context.Context, target *domain.Target, password string) error {
	db, err := Open(target, password)
	if err != nil {
		return err
	}
	defer db.Close()
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_type = 'BASE TABLE' AND COALESCE(engine,'') <> 'InnoDB'`, target.DBName).Scan(&count); err != nil {
		return redacted(err, password)
	}
	if count > 0 {
		return fmt.Errorf("检测到 %d 张非 InnoDB 表；单事务导出无法保证其一致性，当前不执行此备份", count)
	}
	return nil
}

func redacted(err error, password string) error {
	if err == nil {
		return nil
	}
	text := err.Error()
	if password != "" {
		text = strings.ReplaceAll(text, password, "******")
	}
	return fmt.Errorf("%s", text)
}
