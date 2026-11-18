package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/weigong/dbbackups/internal/domain"
	"github.com/weigong/dbbackups/internal/mysql"
	"github.com/weigong/dbbackups/internal/pg"
	"github.com/weigong/dbbackups/internal/secrets"
	"github.com/weigong/dbbackups/internal/storage"
	"github.com/weigong/dbbackups/internal/store"
)

// advisory lock 的键空间前缀，避免与同库上的其他应用冲突。
const advisoryPrefix int64 = 0x4442424B << 32

type Runner struct {
	Store      *store.Store
	Keys       *secrets.Keyring
	Tools      *pg.Toolchain
	MySQLTools *mysql.Toolchain
	Blob       storage.Storage
	Logs       *LogHub
	DataDir    string

	sem    chan struct{}
	mu     sync.Mutex
	busy   map[int64]bool
	cancel map[int64]context.CancelFunc
}

func NewRunner(st *store.Store, keys *secrets.Keyring, tools *pg.Toolchain, mysqlTools *mysql.Toolchain,
	blob storage.Storage, logs *LogHub, dataDir string, maxConcurrent int) *Runner {
	if maxConcurrent <= 0 {
		maxConcurrent = 4
	}
	return &Runner{
		Store: st, Keys: keys, Tools: tools, MySQLTools: mysqlTools, Blob: blob, Logs: logs, DataDir: dataDir,
		sem:    make(chan struct{}, maxConcurrent),
		busy:   make(map[int64]bool),
		cancel: make(map[int64]context.CancelFunc),
	}
}

// Submit 异步执行，使用 Background 上下文：HTTP 请求结束不应中断备份。
func (r *Runner) Submit(jobID int64) {
	go func() {
		if err := r.Run(context.Background(), jobID); err != nil {
			r.Logs.Publish(jobID, "[error] "+err.Error())
		}
	}()
}

func (r *Runner) Cancel(jobID int64) {
	r.mu.Lock()
	fn := r.cancel[jobID]
	r.mu.Unlock()
	if fn != nil {
		fn()
	}
}

type jobLogger struct {
	hub *LogHub
	id  int64
	f   *os.File
	mu  sync.Mutex
}

func (r *Runner) newJobLogger(jobID int64) *jobLogger {
	dir := filepath.Join(r.DataDir, "logs")
	_ = os.MkdirAll(dir, 0o750)
	f, err := os.OpenFile(filepath.Join(dir, fmt.Sprintf("job-%d.log", jobID)),
		os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		f = nil
	}
	return &jobLogger{hub: r.Logs, id: jobID, f: f}
}

func (l *jobLogger) Printf(format string, a ...any) {
	line := fmt.Sprintf(format, a...)
	l.mu.Lock()
	l.hub.Publish(l.id, line)
	if l.f != nil {
		_, _ = l.f.WriteString(time.Now().Format("15:04:05") + " " + line + "\n")
	}
	l.mu.Unlock()
}

func (l *jobLogger) Close() {
	if l.f != nil {
		_ = l.f.Close()
	}
}

func failf(code, format string, a ...any) error {
	return fmt.Errorf("%s: %s", code, fmt.Sprintf(format, a...))
}

func splitCode(err error) (string, string) {
	if err == nil {
		return "", ""
	}
	s := err.Error()
	if i := strings.Index(s, ": "); i > 0 {
		head := s[:i]
		if head != "" && strings.Trim(head, "ABCDEFGHIJKLMNOPQRSTUVWXYZ_") == "" {
			return head, s[i+2:]
		}
	}
	return "INTERNAL", s
}

func elapsedMS(from time.Time) int64 {
	if from.IsZero() {
		return 0
	}
	return time.Since(from).Milliseconds()
}

// Run 执行一次备份的完整流水线。
func (r *Runner) Run(ctx context.Context, jobID int64) (retErr error) {
	job, err := r.Store.GetJob(ctx, jobID)
	if err != nil {
		return fmt.Errorf("加载任务失败: %w", err)
	}
	if job.Status != domain.JobPending {
		return nil
	}
	target, err := r.Store.GetTarget(ctx, job.TargetID)
	if err != nil {
		return fmt.Errorf("加载目标库失败: %w", err)
	}

	log := r.newJobLogger(jobID)
	defer log.Close()
	defer r.Logs.Release(jobID)

	p, perr := r.Store.PolicyByTarget(ctx, target.ID)
	if perr != nil && !errors.Is(perr, store.ErrNotFound) {
		return perr
	}
	if p == nil {
		p = &domain.Policy{
			DumpFormat: domain.FormatCustom, Compression: "zstd:5",
			KeepMinCount: 3, VerifyMode: "toc", TimeoutSeconds: 7200,
		}
		if target.Engine != domain.EnginePostgres {
			p.DumpFormat = domain.FormatSQL
			p.Compression = "gzip:6"
			p.VerifyMode = "checksum"
		}
	}

	ctx, stop := context.WithCancel(ctx)
	defer stop()
	r.mu.Lock()
	r.cancel[jobID] = stop
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.cancel, jobID)
		r.mu.Unlock()
	}()

	var (
		startedAt time.Time
		sizeBytes int64
		sha256Sum string
		toolVer   string
		serverVer = target.ServerVersion
		scratch   string
		code, msg string
	)
	defer func() {
		if retErr != nil {
			code, msg = splitCode(retErr)
			if errors.Is(ctx.Err(), context.Canceled) {
				code, msg = "CANCELED", "任务已取消"
			}
		}
		if code == "" && retErr == nil {
			code = ""
		}
		if retErr != nil || code != "" {
			status := domain.JobFailed
			if code == "CANCELED" {
				status = domain.JobCanceled
			}
			_ = r.Store.FinishJob(context.Background(), jobID, store.JobResult{
				Status: status, SizeBytes: sizeBytes, SHA256: sha256Sum,
				ToolVersion: toolVer, ServerVersion: serverVer,
				ErrorCode: code, ErrorMessage: msg,
				DurationMS: elapsedMS(startedAt),
			})
		}
		if scratch != "" {
			_ = os.Remove(scratch)
		}
	}()

	// 并发闸门：限制同时 dump 的数量，避免把源库 IO 打满
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		return failf("CANCELED", "排队期间被取消")
	}

	// 同目标互斥：进程内 + 数据库 advisory lock 双保险
	r.mu.Lock()
	if r.busy[target.ID] {
		r.mu.Unlock()
		return failf("LOCKED", "同一目标库已有任务在执行")
	}
	r.busy[target.ID] = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.busy, target.ID)
		r.mu.Unlock()
	}()

	release, ok, err := r.acquireTargetLock(ctx, target.ID)
	if err != nil {
		return fmt.Errorf("获取分布式锁失败: %w", err)
	}
	if !ok {
		log.Printf("[skip] 另一实例正在备份 %s，本次跳过", target.Name)
		_ = r.Store.FinishJob(context.Background(), jobID, store.JobResult{
			Status: domain.JobCanceled, ErrorCode: "LOCKED",
			ErrorMessage: "另一实例正在执行同一目标库的备份",
		})
		return nil
	}
	defer release()

	running, err := r.Store.MarkJobRunning(ctx, jobID)
	if err != nil {
		return err
	}
	if !running {
		log.Printf("[skip] 任务在排队期间已被取消")
		return nil
	}
	startedAt = time.Now()

	timeout := time.Duration(p.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 2 * time.Hour
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	log.Printf("开始备份 %s (%s)", target.Name, target.Engine)

	// 1) 凭据解密
	enc, err := r.Store.GetTargetSecret(ctx, target.ID)
	if err != nil {
		return fmt.Errorf("读取凭据失败: %w", err)
	}
	pw, err := r.Keys.Decrypt(enc)
	if err != nil {
		return fmt.Errorf("凭据解密失败（主密钥是否变更？）: %w", err)
	}
	conn := pg.ConnInfo{
		Host: target.Host, Port: target.Port, DBName: target.DBName,
		User: target.Username, Password: pw, SSLMode: target.SSLMode,
	}

	// 2) 预检：版本、容量、权限
	var info *pg.ServerInfo
	if target.Engine == domain.EnginePostgres {
		info, err = pg.Probe(runCtx, conn)
	} else {
		info, err = mysql.Probe(runCtx, target, pw)
	}
	if err != nil {
		return failf(pg.ErrConnectFailed, "%s", strings.ReplaceAll(err.Error(), pw, "******"))
	}
	serverVer = info.Version
	_ = r.Store.UpdateTargetProbe(ctx, target.ID, info.Version, info.VersionNum, info.DBSizeBytes)
	log.Printf("目标库 %s %s（%d 张表，%.2f MiB）",
		target.Engine, info.Version, info.TableCount, float64(info.DBSizeBytes)/1024/1024)
	if info.UnreadableTbl > 0 {
		log.Printf("[warn] 有 %d 张表当前账号无 SELECT 权限，这些表的内容不会被备份", info.UnreadableTbl)
	}

	scratchDir := filepath.Join(r.DataDir, "scratch")
	if err := os.MkdirAll(scratchDir, 0o750); err != nil {
		return fmt.Errorf("准备暂存目录失败: %w", err)
	}
	ext := ".dump"
	major := 0
	var res *pg.DumpResult
	onLog := func(line string) {
		if strings.TrimSpace(line) != "" {
			log.Printf("%s", line)
		}
	}
	if target.Engine == domain.EnginePostgres {
		bin, clientMajor, pickErr := r.Tools.Pick(info.Major)
		if pickErr != nil {
			return failf(pg.ErrToolMissing, "%s", pickErr)
		}
		major = clientMajor
		zipArg, actualZip, zipErr := pg.CompressArg(p.Compression, major)
		if zipErr != nil {
			return failf("CONFIG", "%s", zipErr)
		}
		if actualZip != p.Compression {
			log.Printf("[warn] 压缩调整为 %s", actualZip)
		}
		log.Printf("使用 %s（主版本 %d），压缩 %s", filepath.Base(bin), major, actualZip)
		scratch = filepath.Join(scratchDir, fmt.Sprintf("job-%d%s", jobID, ext))
		res, err = pg.Dump(runCtx, pg.DumpOptions{Bin: bin, Major: major, Conn: conn, OutPath: scratch, Compress: zipArg, LockWaitSeconds: 30, OnLog: onLog})
	} else {
		tool, pickErr := r.MySQLTools.Pick(target.Engine, info.Version)
		if pickErr != nil {
			return failf(pg.ErrToolMissing, "%s", pickErr)
		}
		if checkErr := mysql.CheckTransactionalTables(runCtx, target, pw); checkErr != nil {
			return failf("PREFLIGHT", "%s", checkErr)
		}
		if p.DumpFormat != domain.FormatSQL || p.VerifyMode == "toc" {
			return failf("CONFIG", "MySQL/MariaDB 需要 SQL 格式和校验和校验")
		}
		ext = ".sql"
		if p.Compression != "none" {
			ext += ".gz"
		}
		scratch = filepath.Join(scratchDir, fmt.Sprintf("job-%d%s", jobID, ext))
		log.Printf("使用 %s %s，压缩 %s；备份期间请勿执行 DDL", filepath.Base(tool.Path), tool.Version, p.Compression)
		res, err = mysql.Dump(runCtx, tool, target, pw, scratch, p.Compression, onLog)
	}
	if err != nil {
		return err
	}
	sizeBytes = res.SizeBytes
	sha256Sum = res.SHA256
	toolVer = res.ToolVersion
	log.Printf("dump 完成：%.2f MiB，用时 %.1fs，sha256=%s",
		float64(res.SizeBytes)/1024/1024, float64(res.DurationMS)/1000, res.SHA256[:16])

	// 5) 上传（可选加密）
	objKey, objSize, err := r.upload(runCtx, jobID, target.ID, target.Engine, ext, scratch, p.Encrypt)
	if err != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return failf("TIMEOUT", "上传期间超时")
		}
		return failf("UPLOAD_FAILED", "%s", err.Error())
	}
	if _, err := r.Store.CreateArtifact(ctx, jobID, objKey, objSize, res.SHA256, p.Encrypt); err != nil {
		return fmt.Errorf("记录备份产物失败: %w", err)
	}
	log.Printf("已上传 %s（%.2f MiB，加密=%v）", objKey, float64(objSize)/1024/1024, p.Encrypt)

	// 6) 校验
	verr := r.verify(runCtx, jobID, scratch, major, p.VerifyMode, log)
	if runCtx.Err() != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return failf("TIMEOUT", "校验期间超时")
		}
		return failf("CANCELED", "任务已取消")
	}

	_ = r.Store.FinishJob(ctx, jobID, store.JobResult{
		Status: domain.JobSuccess, SizeBytes: res.SizeBytes, SHA256: res.SHA256,
		ToolVersion: res.ToolVersion, ServerVersion: info.Version,
		DurationMS: elapsedMS(startedAt),
	})
	log.Printf("[ok] 备份成功")

	if verr != nil {
		log.Printf("[warn] 校验未通过：%s", verr.Error())
	}

	// 7) 异步 GC，不拖慢任务收尾
	go func() {
		gctx, gcancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer gcancel()
		if _, err := r.GC(gctx, target.ID); err != nil {
			r.Logs.Publish(jobID, "[warn] 保留策略执行失败: "+err.Error())
		}
	}()
	return nil
}

func (r *Runner) acquireTargetLock(ctx context.Context, targetID int64) (func(), bool, error) {
	conn, err := r.Store.Pool().Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	key := advisoryPrefix | (targetID & 0xFFFFFFFF)
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&got); err != nil {
		conn.Release()
		return nil, false, err
	}
	if !got {
		conn.Release()
		return func() {}, false, nil
	}
	return func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, key)
		conn.Release()
	}, true, nil
}

func (r *Runner) upload(ctx context.Context, jobID, targetID int64, engine, extension, path string, encrypt bool) (string, int64, error) {
	key := fmt.Sprintf("%s/%d/%s/%d%s", engine, targetID, time.Now().Format("2006/01"), jobID, extension)
	if encrypt {
		key += ".enc"
	}

	if !encrypt {
		f, err := os.Open(path)
		if err != nil {
			return "", 0, err
		}
		defer f.Close()
		if err := r.Blob.Put(ctx, key, f, 0); err != nil {
			return "", 0, err
		}
		size, _ := r.Blob.Stat(ctx, key)
		return key, size, nil
	}

	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		f, err := os.Open(path)
		if err != nil {
			pw.CloseWithError(err)
			done <- err
			return
		}
		defer f.Close()
		w, err := r.Keys.NewEncWriter(pw)
		if err != nil {
			pw.CloseWithError(err)
			done <- err
			return
		}
		if _, err := io.Copy(w, f); err != nil {
			_ = w.Close()
			pw.CloseWithError(err)
			done <- err
			return
		}
		if err := w.Close(); err != nil {
			pw.CloseWithError(err)
			done <- err
			return
		}
		pw.Close()
		done <- nil
	}()

	putErr := r.Blob.Put(ctx, key, pr, 0)
	if putErr != nil {
		_ = pr.CloseWithError(putErr)
	}
	wErr := <-done
	if putErr != nil {
		_ = r.Blob.Delete(ctx, key)
		return "", 0, putErr
	}
	if wErr != nil {
		_ = r.Blob.Delete(ctx, key)
		return "", 0, wErr
	}
	size, err := r.Blob.Stat(ctx, key)
	if err != nil {
		return "", 0, err
	}
	return key, size, nil
}

func (r *Runner) verify(ctx context.Context, jobID int64, path string, major int, mode string, log *jobLogger) error {
	if mode == "" || mode == "none" {
		return nil
	}
	start := time.Now()
	report := map[string]any{"mode": mode}
	status := "success"
	var vErr error

	if mode == "checksum" {
		report["note"] = "dump 完成后已计算 sha256"
	} else {
		bin, err := r.Tools.RestorePath(major)
		if err != nil {
			vErr = err
		} else {
			cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(cctx, bin, "--list", path)
			out, err := cmd.Output()
			if err != nil {
				vErr = fmt.Errorf("pg_restore --list 失败: %w", err)
			} else {
				entries := strings.Count(string(out), "\n")
				report["toc_entries"] = entries
				if entries < 5 {
					vErr = fmt.Errorf("TOC 条目仅 %d 条，归档可能不完整", entries)
				}
				log.Printf("TOC 校验通过：%d 个条目", entries)
			}
		}
	}

	if vErr != nil {
		status = "failed"
		report["error"] = vErr.Error()
	}
	body, _ := json.Marshal(report)
	_ = r.Store.CreateVerification(context.Background(), jobID, mode, status,
		time.Since(start).Milliseconds(), body)
	return vErr
}

type GCResult struct {
	Scanned    int    `json:"scanned"`
	Deleted    int    `json:"deleted"`
	FreedBytes int64  `json:"freed_bytes"`
	Skipped    string `json:"skipped,omitempty"`
}

// GC 按目标库执行保留策略。
// 顺序：先在库里标记 deleted，再删对象。这样即使删对象失败，元数据也不会指向
// 一个被删掉的文件（宁可泄漏空间，不可制造坏引用）。
func (r *Runner) GC(ctx context.Context, targetID int64) (*GCResult, error) {
	p, err := r.Store.PolicyByTarget(ctx, targetID)
	if errors.Is(err, store.ErrNotFound) {
		return &GCResult{Skipped: "该库没有策略，跳过清理"}, nil
	}
	if err != nil {
		return nil, err
	}

	arts, err := r.Store.LiveArtifactsByTarget(ctx, targetID)
	if err != nil {
		return nil, err
	}
	cands := make([]Candidate, 0, len(arts))
	for _, a := range arts {
		cands = append(cands, Candidate{
			ID: a.ID, CreatedAt: a.CreatedAt, SizeBytes: a.SizeBytes,
			JobStatus: a.JobStatus, ObjectKey: a.ObjectKey,
		})
	}

	plan := PlanRetention(cands, RetentionRule{
		Count: p.RetainCount, Days: p.RetainDays, MaxBytes: p.RetainMaxBytes, KeepMin: p.KeepMinCount,
	}, time.Now())

	res := &GCResult{Scanned: len(cands)}
	if len(plan.Kill) == 0 {
		return res, nil
	}
	// 最后一道闸：如果保留下来的全是失败备份，说明备份已经连续出问题，
	// 此时删旧备份等于自断后路，必须先告警而不是清理。
	if !plan.HasSuccessful() {
		res.Skipped = "保留集合中没有成功备份，已暂停清理"
		return res, nil
	}

	for _, c := range plan.Kill {
		// 先在元数据库标记，再删对象：宁可残留文件，也不制造指向空文件的坏引用。
		if err := r.Store.MarkArtifactDeleted(ctx, c.ID); err != nil {
			continue
		}
		if c.ObjectKey != "" {
			if err := r.Blob.Delete(ctx, c.ObjectKey); err != nil {
				// 标记成功但对象删除失败：空间暂时泄漏，需人工或后续清理介入。
				slog.Error("删除备份对象失败", "artifact", c.ID, "key", c.ObjectKey, "err", err)
				continue
			}
		}
		res.Deleted++
		res.FreedBytes += c.SizeBytes
	}
	return res, nil
}
