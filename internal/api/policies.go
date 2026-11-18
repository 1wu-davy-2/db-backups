package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/weigong/dbbackups/internal/backup"
	"github.com/weigong/dbbackups/internal/domain"
	"github.com/weigong/dbbackups/internal/mysql"
	"github.com/weigong/dbbackups/internal/pg"
	"github.com/weigong/dbbackups/internal/store"
)

type policyReq struct {
	TargetID       int64  `json:"target_id"`
	CronExpr       string `json:"cron_expr"`
	Timezone       string `json:"timezone"`
	DumpFormat     string `json:"dump_format"`
	Compression    string `json:"compression"`
	Encrypt        *bool  `json:"encrypt"`
	RetainCount    int    `json:"retain_count"`
	RetainDays     int    `json:"retain_days"`
	RetainMaxBytes int64  `json:"retain_max_bytes"`
	KeepMinCount   int    `json:"keep_min_count"`
	VerifyMode     string `json:"verify_mode"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	MisfirePolicy  string `json:"misfire_policy"`
	IsEnabled      *bool  `json:"is_enabled"`
}

func (p *policyReq) normalize(engine string) error {
	p.CronExpr = strings.TrimSpace(p.CronExpr)
	if p.CronExpr == "" {
		return errors.New("cron 表达式必填")
	}
	if p.Timezone == "" {
		p.Timezone = "Asia/Shanghai"
	}
	format, compression, verify := domain.FormatCustom, "zstd:5", "toc"
	if engine != domain.EnginePostgres {
		format, compression, verify = domain.FormatSQL, "gzip:6", "checksum"
	}
	if p.DumpFormat == "" {
		p.DumpFormat = format
	}
	if p.DumpFormat != format {
		return errors.New("该引擎仅支持 " + format + " 格式")
	}
	if p.Compression == "" {
		p.Compression = compression
	}
	if engine == domain.EnginePostgres {
		if _, _, err := pg.CompressArg(p.Compression, 18); err != nil {
			return err
		}
	} else {
		if _, err := mysql.CompressionLevel(p.Compression); err != nil {
			return err
		}
		if p.VerifyMode == "toc" {
			return errors.New("SQL 导出不支持 TOC 校验")
		}
	}
	if p.VerifyMode == "" {
		p.VerifyMode = verify
	}
	if p.RetainCount <= 0 && p.RetainDays <= 0 && p.RetainMaxBytes <= 0 {
		return errors.New("保留策略至少设置一项：份数 / 天数 / 容量上限")
	}
	if p.RetainCount < 0 || p.RetainDays < 0 || p.RetainMaxBytes < 0 {
		return errors.New("保留参数不能为负")
	}
	if p.KeepMinCount <= 0 {
		p.KeepMinCount = 3
	}
	if p.VerifyMode == "" {
		p.VerifyMode = "toc"
	}
	switch p.VerifyMode {
	case "none", "checksum", "toc":
	default:
		return errors.New("不支持的校验模式")
	}
	if p.TimeoutSeconds <= 0 {
		p.TimeoutSeconds = 7200
	}
	if p.TimeoutSeconds < 60 || p.TimeoutSeconds > 86400 {
		return errors.New("超时时间应在 60 ~ 86400 秒之间")
	}
	if p.MisfirePolicy == "" {
		p.MisfirePolicy = "skip"
	}
	if p.MisfirePolicy != "skip" && p.MisfirePolicy != "run_once" {
		return errors.New("不支持的错过策略")
	}
	return nil
}

func (s *Server) handleListPolicies(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListPolicies(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询策略失败")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreatePolicy(w http.ResponseWriter, r *http.Request) {
	if !s.requireWrite(w, r) {
		return
	}
	var req policyReq
	if !decodeJSON(w, r, &req) {
		return
	}
	t, err := s.Store.GetTarget(r.Context(), req.TargetID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "目标库不存在")
		return
	}
	if err := req.normalize(t.Engine); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := backup.ValidateCron(req.CronExpr, req.Timezone); err != nil {
		writeErr(w, http.StatusBadRequest, "cron 表达式非法："+err.Error())
		return
	}

	// M1 约束：一个目标库只能有一条策略，避免多策略保留规则互相打架导致误删。
	n, err := s.Store.PolicyCountForTarget(r.Context(), req.TargetID, 0)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "校验策略失败")
		return
	}
	if n > 0 {
		writeErr(w, http.StatusConflict, "该库已有备份策略，请直接编辑现有策略")
		return
	}

	encrypt, enabled := true, true
	if req.Encrypt != nil {
		encrypt = *req.Encrypt
	}
	if req.IsEnabled != nil {
		enabled = *req.IsEnabled
	}
	p := &domain.Policy{
		TargetID: req.TargetID, CronExpr: req.CronExpr, Timezone: req.Timezone,
		Kind: "logical", DumpFormat: req.DumpFormat, Compression: req.Compression,
		Encrypt: encrypt, RetainCount: req.RetainCount, RetainDays: req.RetainDays,
		RetainMaxBytes: req.RetainMaxBytes, KeepMinCount: req.KeepMinCount,
		VerifyMode: req.VerifyMode, TimeoutSeconds: req.TimeoutSeconds,
		MisfirePolicy: req.MisfirePolicy, IsEnabled: enabled,
	}
	created, err := s.Store.CreatePolicy(r.Context(), p)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "保存策略失败")
		return
	}
	_ = s.Sched.Reload(r.Context())
	// Reload 会写入 next_run_at，重新读取一次让前端立刻看到下次执行时间
	if fresh, err := s.Store.GetPolicy(r.Context(), created.ID); err == nil {
		created = fresh
	}
	s.audit(r, "policy.create", "target:"+t.Name, map[string]any{
		"cron": req.CronExpr, "retain_count": req.RetainCount, "retain_days": req.RetainDays,
	})
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleUpdatePolicy(w http.ResponseWriter, r *http.Request) {
	if !s.requireWrite(w, r) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "非法 ID")
		return
	}
	existing, err := s.Store.GetPolicy(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "策略不存在")
		return
	}
	var req policyReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.TargetID == 0 {
		req.TargetID = existing.TargetID
	}
	if req.TargetID != existing.TargetID {
		writeErr(w, http.StatusBadRequest, "不能更改策略所属目标库")
		return
	}
	t, err := s.Store.GetTarget(r.Context(), req.TargetID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "目标库不存在")
		return
	}
	if err := req.normalize(t.Engine); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := backup.ValidateCron(req.CronExpr, req.Timezone); err != nil {
		writeErr(w, http.StatusBadRequest, "cron 表达式非法："+err.Error())
		return
	}

	encrypt, enabled := existing.Encrypt, existing.IsEnabled
	if req.Encrypt != nil {
		encrypt = *req.Encrypt
	}
	if req.IsEnabled != nil {
		enabled = *req.IsEnabled
	}
	p := &domain.Policy{
		ID: id, TargetID: req.TargetID, CronExpr: req.CronExpr, Timezone: req.Timezone,
		Kind: "logical", DumpFormat: req.DumpFormat, Compression: req.Compression,
		Encrypt: encrypt, RetainCount: req.RetainCount, RetainDays: req.RetainDays,
		RetainMaxBytes: req.RetainMaxBytes, KeepMinCount: req.KeepMinCount,
		VerifyMode: req.VerifyMode, TimeoutSeconds: req.TimeoutSeconds,
		MisfirePolicy: req.MisfirePolicy, IsEnabled: enabled,
	}
	updated, err := s.Store.UpdatePolicy(r.Context(), p)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "保存策略失败")
		return
	}
	_ = s.Sched.Reload(r.Context())
	if fresh, err := s.Store.GetPolicy(r.Context(), id); err == nil {
		updated = fresh
	}
	s.audit(r, "policy.update", "policy:"+itoa(id), map[string]any{
		"cron": req.CronExpr, "retain_count": req.RetainCount,
		"retain_days": req.RetainDays, "retain_max_bytes": req.RetainMaxBytes,
	})
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeletePolicy(w http.ResponseWriter, r *http.Request) {
	if !s.requireWrite(w, r) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "非法 ID")
		return
	}
	if err := s.Store.DeletePolicy(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "策略不存在")
			return
		}
		writeErr(w, http.StatusInternalServerError, "删除失败")
		return
	}
	_ = s.Sched.Reload(r.Context())
	s.audit(r, "policy.delete", "policy:"+itoa(id), nil)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
