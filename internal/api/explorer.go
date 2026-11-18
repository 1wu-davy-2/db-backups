package api

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/weigong/dbbackups/internal/explorer"
)

type exploration struct {
	RequestID string
	TargetID  int64
	Cancel    context.CancelFunc
}
type exploreRequest struct {
	RequestID string `json:"request_id"`
	Action    string `json:"action"`
	SQL       string `json:"sql"`
	Filter    string `json:"filter"`
	explorer.DataRequest
}

func (s *Server) allowExplore(w http.ResponseWriter, r *http.Request) bool {
	if !s.requireWrite(w, r) {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeErr(w, 415, "请发送 JSON 请求")
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host != r.Host {
			writeErr(w, 403, "不允许跨站查询")
			return false
		}
	}
	return true
}

func (s *Server) handleExplore(w http.ResponseWriter, r *http.Request) {
	if !s.allowExplore(w, r) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "非法 ID")
		return
	}
	var req exploreRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.RequestID) < 8 || len(req.RequestID) > 80 {
		writeErr(w, 400, "查询标识非法")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	uid := userFrom(r).ID
	s.queryMu.Lock()
	if s.queries == nil {
		s.queries = make(map[int64]exploration)
	}
	_, busy := s.queries[uid]
	if busy || len(s.queries) >= 4 {
		s.queryMu.Unlock()
		writeErr(w, 429, "已有查询执行中，请等待或取消后重试")
		return
	}
	s.queries[uid] = exploration{req.RequestID, id, cancel}
	s.queryMu.Unlock()
	defer func() { s.queryMu.Lock(); delete(s.queries, uid); s.queryMu.Unlock() }()
	target, err := s.Store.GetTarget(ctx, id)
	if err != nil {
		writeErr(w, 404, "目标库不存在")
		return
	}
	if !target.IsEnabled {
		writeErr(w, 409, "目标库已停用")
		return
	}
	enc, err := s.Store.GetTargetSecret(ctx, id)
	if err != nil {
		writeErr(w, 500, "读取凭据失败")
		return
	}
	password, err := s.Keys.Decrypt(enc)
	if err != nil {
		writeErr(w, 500, "凭据解密失败")
		return
	}
	start := time.Now()
	var result any
	switch req.Action {
	case "tables":
		result, err = explorer.Tables(ctx, target, password, req.Filter)
	case "structure":
		result, err = explorer.Describe(ctx, target, password, req.Schema, req.Table)
	case "data":
		result, err = explorer.Data(ctx, target, password, req.DataRequest)
	case "query":
		result, err = explorer.Query(ctx, target, password, req.SQL)
	default:
		writeErr(w, 400, "未知的数据浏览操作")
		return
	}
	status := "success"
	if err != nil {
		status = "failed"
	}
	if ctx.Err() != nil {
		status = "canceled"
	}
	detail := map[string]any{"status": status, "duration_ms": time.Since(start).Milliseconds(), "request_id": req.RequestID}
	if req.Action == "query" {
		detail["sql_sha256"] = fmt.Sprintf("%x", sha256.Sum256([]byte(req.SQL)))
	}
	if data, ok := result.(*explorer.Result); ok && data != nil {
		detail["rows"] = len(data.Rows)
	}
	auditCtx, stop := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
	defer stop()
	s.audit(r.WithContext(auditCtx), "explorer."+req.Action, "target:"+target.Name, detail)
	if err != nil {
		text := err.Error()
		if password != "" {
			text = strings.ReplaceAll(text, password, "******")
		}
		if ctx.Err() != nil {
			text = "查询已取消或超时"
		}
		writeErr(w, 400, text)
		return
	}
	writeJSON(w, 200, result)
}

func (s *Server) handleCancelExplore(w http.ResponseWriter, r *http.Request) {
	if !s.allowExplore(w, r) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "非法 ID")
		return
	}
	var req struct {
		RequestID string `json:"request_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	s.queryMu.Lock()
	active, ok := s.queries[userFrom(r).ID]
	if ok && active.TargetID == id && active.RequestID == req.RequestID {
		active.Cancel()
	} else {
		ok = false
	}
	s.queryMu.Unlock()
	if !ok {
		writeErr(w, 409, "查询已结束或不存在")
		return
	}
	s.audit(r, "explorer.cancel", "target:"+itoa(id), map[string]any{"request_id": req.RequestID})
	writeJSON(w, 200, map[string]bool{"ok": true})
}
