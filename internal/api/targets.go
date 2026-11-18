package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/weigong/dbbackups/internal/domain"
	"github.com/weigong/dbbackups/internal/mysql"
	"github.com/weigong/dbbackups/internal/pg"
	"github.com/weigong/dbbackups/internal/store"
)

type targetReq struct {
	Engine    string   `json:"engine"`
	Name      string   `json:"name"`
	Host      string   `json:"host"`
	Port      int      `json:"port"`
	DBName    string   `json:"dbname"`
	Username  string   `json:"username"`
	Password  string   `json:"password"`
	SSLMode   string   `json:"ssl_mode"`
	Tags      []string `json:"tags"`
	IsEnabled *bool    `json:"is_enabled"`
}

func (t *targetReq) normalize() error {
	t.Name = strings.TrimSpace(t.Name)
	t.Host = strings.TrimSpace(t.Host)
	t.DBName = strings.TrimSpace(t.DBName)
	t.Username = strings.TrimSpace(t.Username)
	if t.Name == "" {
		return errors.New("名称必填")
	}
	if t.Host == "" {
		return errors.New("主机必填")
	}
	if t.DBName == "" {
		return errors.New("数据库名必填")
	}
	if t.Username == "" {
		return errors.New("用户名必填")
	}
	if t.Engine == "" {
		t.Engine = domain.EnginePostgres
	}
	switch t.Engine {
	case domain.EnginePostgres, domain.EngineMySQL, domain.EngineMariaDB:
	default:
		return errors.New("不支持的数据库类型")
	}
	if t.Port == 0 {
		t.Port = 5432
		if t.Engine != domain.EnginePostgres {
			t.Port = 3306
		}
	}
	if t.Port < 1 || t.Port > 65535 {
		return errors.New("端口非法")
	}
	if t.SSLMode == "" {
		t.SSLMode = "prefer"
		if t.Engine != domain.EnginePostgres {
			t.SSLMode = "verify-full"
		}
	}
	if t.Tags == nil {
		t.Tags = []string{}
	}
	return nil
}

func probeTarget(ctx context.Context, target *domain.Target, password string) (*pg.ServerInfo, error) {
	if target.Engine == domain.EngineMySQL || target.Engine == domain.EngineMariaDB {
		return mysql.Probe(ctx, target, password)
	}
	return pg.Probe(ctx, pg.ConnInfo{Host: target.Host, Port: target.Port, DBName: target.DBName, User: target.Username, Password: password, SSLMode: target.SSLMode})
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	return id, err == nil && id > 0
}

func (s *Server) handleListTargets(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListTargets(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询目标库失败")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleGetTarget(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "非法 ID")
		return
	}
	t, err := s.Store.GetTarget(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "目标库不存在")
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleCreateTarget(w http.ResponseWriter, r *http.Request) {
	if !s.requireWrite(w, r) {
		return
	}
	var req targetReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := req.normalize(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Password == "" {
		writeErr(w, http.StatusBadRequest, "密码必填")
		return
	}

	enabled := true
	if req.IsEnabled != nil {
		enabled = *req.IsEnabled
	}
	t := &domain.Target{
		Name: req.Name, Engine: req.Engine, Host: req.Host, Port: req.Port,
		DBName: req.DBName, Username: req.Username, SSLMode: req.SSLMode,
		Tags: req.Tags, IsEnabled: enabled,
	}

	// 创建前先真连一次：避免把连不上的配置存进库，等到凌晨备份时才炸。
	info, err := probeTarget(r.Context(), t, req.Password)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "连接测试失败："+err.Error())
		return
	}
	t.ServerVersion = info.Version
	t.ServerVerNum = info.VersionNum
	t.DBSizeBytes = info.DBSizeBytes

	enc, err := s.Keys.Encrypt(req.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "加密凭据失败")
		return
	}
	created, err := s.Store.CreateTarget(r.Context(), t, enc)
	if err != nil {
		if strings.Contains(err.Error(), "targets_name_uq") {
			writeErr(w, http.StatusConflict, "名称已存在")
			return
		}
		writeErr(w, http.StatusInternalServerError, "保存失败")
		return
	}
	s.audit(r, "target.create", "target:"+created.Name, map[string]any{
		"host": created.Host, "dbname": created.DBName, "version": created.ServerVersion,
	})
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleUpdateTarget(w http.ResponseWriter, r *http.Request) {
	if !s.requireWrite(w, r) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "非法 ID")
		return
	}
	existing, err := s.Store.GetTarget(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "目标库不存在")
		return
	}
	var req targetReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := req.normalize(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	password := req.Password
	if password == "" {
		enc, err := s.Store.GetTargetSecret(r.Context(), id)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "读取凭据失败")
			return
		}
		password, err = s.Keys.Decrypt(enc)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "凭据解密失败")
			return
		}
	}

	if req.Engine != existing.Engine {
		writeErr(w, http.StatusBadRequest, "不能更改已有目标库的类型，请创建新目标库")
		return
	}
	info, err := probeTarget(r.Context(), &domain.Target{Engine: req.Engine, Host: req.Host, Port: req.Port, DBName: req.DBName, Username: req.Username, SSLMode: req.SSLMode}, password)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "连接测试失败："+err.Error())
		return
	}
	_ = s.Store.UpdateTargetProbe(r.Context(), id, info.Version, info.VersionNum, info.DBSizeBytes)

	enabled := existing.IsEnabled
	if req.IsEnabled != nil {
		enabled = *req.IsEnabled
	}
	upd := &domain.Target{
		ID: id, Name: req.Name, Engine: req.Engine, Host: req.Host, Port: req.Port,
		DBName: req.DBName, Username: req.Username, SSLMode: req.SSLMode,
		Tags: req.Tags, IsEnabled: enabled,
	}

	var enc []byte
	if req.Password != "" {
		if enc, err = s.Keys.Encrypt(req.Password); err != nil {
			writeErr(w, http.StatusInternalServerError, "加密凭据失败")
			return
		}
	}
	out, err := s.Store.UpdateTarget(r.Context(), upd, enc)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "保存失败")
		return
	}
	s.audit(r, "target.update", "target:"+out.Name, map[string]any{"id": id})
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDeleteTarget(w http.ResponseWriter, r *http.Request) {
	if !s.requireWrite(w, r) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "非法 ID")
		return
	}
	t, err := s.Store.GetTarget(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "目标库不存在")
		return
	}
	if err := s.Store.DeleteTarget(r.Context(), id); err != nil && !errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusInternalServerError, "删除失败")
		return
	}
	s.audit(r, "target.delete", "target:"+t.Name, map[string]any{"id": id})
	_ = s.Sched.Reload(r.Context())
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleTestTarget(w http.ResponseWriter, r *http.Request) {
	if !s.requireWrite(w, r) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "非法 ID")
		return
	}
	t, err := s.Store.GetTarget(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "目标库不存在")
		return
	}
	enc, err := s.Store.GetTargetSecret(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取凭据失败")
		return
	}
	pw, err := s.Keys.Decrypt(enc)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "凭据解密失败")
		return
	}
	info, err := probeTarget(r.Context(), t, pw)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "连接失败："+err.Error())
		return
	}
	_ = s.Store.UpdateTargetProbe(r.Context(), id, info.Version, info.VersionNum, info.DBSizeBytes)
	writeJSON(w, http.StatusOK, map[string]any{
		"version":           info.Version,
		"version_num":       info.VersionNum,
		"db_size_bytes":     info.DBSizeBytes,
		"table_count":       info.TableCount,
		"unreadable_tables": info.UnreadableTbl,
	})
}

func (s *Server) handleManualBackup(w http.ResponseWriter, r *http.Request) {
	if !s.requireWrite(w, r) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "非法 ID")
		return
	}
	t, err := s.Store.GetTarget(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "目标库不存在")
		return
	}
	if !t.IsEnabled {
		writeErr(w, http.StatusBadRequest, "目标库已禁用")
		return
	}

	var policyID *int64
	if p, err := s.Store.PolicyByTarget(r.Context(), id); err == nil {
		policyID = &p.ID
	}
	job, err := s.Store.CreateJob(r.Context(), policyID, id, domain.TriggerManual)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "创建任务失败")
		return
	}
	s.audit(r, "job.trigger", "target:"+t.Name, map[string]any{"job_id": job.ID})
	s.Runner.Submit(job.ID)
	writeJSON(w, http.StatusAccepted, job)
}
