package mysql

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/weigong/dbbackups/internal/domain"
	"github.com/weigong/dbbackups/internal/pg"
)

func ClientConfig(target *domain.Target, password string) (string, error) {
	quote := func(v string) string {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`).Replace(v) + `"`
	}
	for _, value := range []string{target.Host, target.Username, password} {
		if strings.ContainsRune(value, 0) {
			return "", fmt.Errorf("连接参数不能包含 NUL")
		}
	}
	tls := ""
	switch target.SSLMode {
	case "disable":
		if target.Engine == "mariadb" {
			tls = "ssl=0\n"
		} else {
			tls = "ssl-mode=DISABLED\n"
		}
	case "require", "verify-full":
		if target.Engine == "mariadb" {
			tls = "ssl=1\nssl-verify-server-cert=1\n"
		} else {
			tls = "ssl-mode=VERIFY_IDENTITY\n"
		}
	default:
		return "", fmt.Errorf("不支持的 MySQL/MariaDB TLS 模式")
	}
	return fmt.Sprintf("[client]\nprotocol=tcp\nhost=%s\nport=%d\nuser=%s\npassword=%s\n%s", quote(target.Host), target.Port, quote(target.Username), quote(password), tls), nil
}

func DumpArgs(tool Tool, target *domain.Target, configPath string) []string {
	args := []string{"--defaults-file=" + configPath, "--single-transaction", "--quick", "--skip-lock-tables", "--hex-blob", "--routines", "--events", "--triggers", "--default-character-set=utf8mb4", "--verbose"}
	if tool.Engine == "mysql" {
		args = append(args, "--set-gtid-purged=OFF", "--no-tablespaces")
		if tool.Major >= 8 {
			args = append(args, "--column-statistics=0")
		}
	}
	return append(args, "--", target.DBName)
}

func CompressionLevel(spec string) (int, error) {
	if spec == "none" {
		return 0, nil
	}
	kind, value, ok := strings.Cut(spec, ":")
	level, err := strconv.Atoi(value)
	if !ok || kind != "gzip" || err != nil || level < 1 || level > 9 {
		return 0, fmt.Errorf("SQL 导出仅支持 none 或 gzip:1~9")
	}
	return level, nil
}

func Dump(ctx context.Context, tool Tool, target *domain.Target, password, path, compression string, onLog func(string)) (*pg.DumpResult, error) {
	start := time.Now()
	level, err := CompressionLevel(compression)
	if err != nil {
		return nil, err
	}
	text, err := ClientConfig(target, password)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(filepath.Dir(path), "mysql-credentials-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	configPath := filepath.Join(dir, "client.cnf")
	if err := os.WriteFile(configPath, []byte(text), 0600); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var output io.Writer = f
	var zip *gzip.Writer
	if level > 0 {
		zip, err = gzip.NewWriterLevel(f, level)
		if err != nil {
			return nil, err
		}
		output = zip
	}
	cmd := exec.CommandContext(ctx, tool.Path, DumpArgs(tool, target, configPath)...)
	cmd.WaitDelay = 3 * time.Second
	cmd.Stdout = output
	reader, writer := io.Pipe()
	cmd.Stderr = writer
	done := make(chan struct{})
	var last []string
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 4096), 64<<10)
		for scanner.Scan() {
			line := scanner.Text()
			if password != "" {
				line = strings.ReplaceAll(line, password, "******")
			}
			last = append(last, line)
			if len(last) > 20 {
				last = last[1:]
			}
			if onLog != nil {
				onLog(line)
			}
		}
		_ = reader.Close()
	}()
	runErr := cmd.Run()
	_ = writer.Close()
	<-done
	if zip != nil {
		if err := zip.Close(); runErr == nil {
			runErr = err
		}
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("TIMEOUT: 导出超时")
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("CANCELED: 导出已取消")
	}
	if runErr != nil {
		return nil, fmt.Errorf("DUMP_FAILED: %v\n%s", redacted(runErr, password), strings.Join(last, "\n"))
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	input, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer input.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, input)
	if err != nil {
		return nil, err
	}
	if size == 0 {
		return nil, fmt.Errorf("DUMP_FAILED: 导出文件为空")
	}
	return &pg.DumpResult{SizeBytes: size, SHA256: fmt.Sprintf("%x", hash.Sum(nil)), ToolVersion: tool.Version, DurationMS: time.Since(start).Milliseconds(), ActualZip: compression}, nil
}
