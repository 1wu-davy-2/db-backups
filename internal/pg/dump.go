package pg

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	ErrToolMissing   = "TOOL_MISSING"
	ErrConnectFailed = "CONNECT_FAILED"
	ErrTimeout       = "TIMEOUT"
	ErrDumpFailed    = "DUMP_FAILED"
	ErrDiskFull      = "DISK_FULL"
	ErrCanceled      = "CANCELED"
)

type DumpOptions struct {
	Bin             string
	Major           int
	Conn            ConnInfo
	OutPath         string
	Compress        string // pg_dump -Z 的取值，如 "zstd:5"
	LockWaitSeconds int
	OnLog           func(string)
}

type DumpResult struct {
	SizeBytes   int64
	SHA256      string
	ToolVersion string
	DurationMS  int64
	ActualZip   string
}

// Dump 执行 pg_dump。
// 使用 -f 而非 stdout 管道：Windows 下二进制流经控制台/管道有换行转换风险，
// 落盘后再读一遍算摘要，换来的是可验证的正确性。
func Dump(ctx context.Context, opt DumpOptions) (*DumpResult, error) {
	if _, err := os.Stat(opt.Bin); err != nil {
		return nil, fmt.Errorf("%s: %w", ErrToolMissing, err)
	}
	lockWait := opt.LockWaitSeconds
	if lockWait <= 0 {
		lockWait = 30
	}

	args := []string{
		"-h", opt.Conn.Host,
		"-p", strconv.Itoa(opt.Conn.Port),
		"-U", opt.Conn.User,
		"-d", opt.Conn.DBName,
		"-Fc",
		"-Z", opt.Compress,
		"--lock-wait-timeout", fmt.Sprintf("%ds", lockWait),
		"--no-owner",
		"--no-privileges",
		"--verbose",
		"-f", opt.OutPath,
	}

	cmd := exec.Command(opt.Bin, args...)
	setProcAttr(cmd)
	cmd.Env = append(os.Environ(),
		"PGPASSWORD="+opt.Conn.Password,
		"PGSSLMODE="+sslOrDefault(opt.Conn.SSLMode),
		"PGCONNECT_TIMEOUT=15",
		"PGAPPNAME=dbbackups",
		// 长时间 dump 需要显式关闭会话级超时，否则被企业策略中途 kill
		"PGOPTIONS=-c statement_timeout=0 -c idle_in_transaction_session_timeout=0",
	)

	pr, pw := io.Pipe()
	cmd.Stderr = pw
	defer pr.Close()

	lastLines := make([]string, 0, 20)
	logDone := make(chan struct{})
	go func() {
		defer close(logDone)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			if opt.OnLog != nil {
				opt.OnLog(line)
			}
			lastLines = append(lastLines, line)
			if len(lastLines) > 20 {
				lastLines = lastLines[1:]
			}
		}
	}()

	start := time.Now()
	if err := cmd.Start(); err != nil {
		pw.Close()
		<-logDone
		return nil, fmt.Errorf("%s: %w", ErrDumpFailed, err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var runErr error
	select {
	case runErr = <-done:
	case <-ctx.Done():
		killTree(cmd)
		<-done
		runErr = ctx.Err()
	}
	_ = pw.Close()
	<-logDone

	dur := time.Since(start).Milliseconds()
	tail := strings.Join(lastLines, "\n")

	if runErr != nil {
		switch {
		case errors.Is(runErr, context.DeadlineExceeded):
			return nil, fmt.Errorf("%s: 超过任务超时时间", ErrTimeout)
		case errors.Is(runErr, context.Canceled):
			return nil, fmt.Errorf("%s: 任务被取消", ErrCanceled)
		}
		code := ErrDumpFailed
		low := strings.ToLower(tail)
		switch {
		case strings.Contains(low, "no space left"), strings.Contains(low, "disk full"):
			code = ErrDiskFull
		case strings.Contains(low, "could not connect"), strings.Contains(low, "connection refused"),
			strings.Contains(low, "password authentication failed"), strings.Contains(low, "timeout expired"):
			code = ErrConnectFailed
		}
		msg := tail
		if msg == "" {
			msg = runErr.Error()
		}
		if len(msg) > 2000 {
			msg = msg[len(msg)-2000:]
		}
		return nil, fmt.Errorf("%s: %s", code, msg)
	}

	st, err := os.Stat(opt.OutPath)
	if err != nil {
		return nil, fmt.Errorf("%s: 未生成备份文件: %w", ErrDumpFailed, err)
	}
	if st.Size() == 0 {
		return nil, fmt.Errorf("%s: 备份文件为空，检查账号权限或库是否存在", ErrDumpFailed)
	}

	sum, err := fileSHA256(opt.OutPath)
	if err != nil {
		return nil, fmt.Errorf("计算摘要失败: %w", err)
	}

	ver, _ := toolVersion(opt.Bin)
	return &DumpResult{
		SizeBytes:   st.Size(),
		SHA256:      sum,
		ToolVersion: ver,
		DurationMS:  dur,
		ActualZip:   opt.Compress,
	}, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func sslOrDefault(s string) string {
	if s == "" {
		return "prefer"
	}
	return s
}
