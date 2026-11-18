package api

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/weigong/dbbackups/internal/domain"
	"github.com/weigong/dbbackups/internal/storage"
	"github.com/weigong/dbbackups/internal/store"
)

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	targetID, _ := strconv.ParseInt(q.Get("target_id"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))

	items, total, err := s.Store.ListJobs(r.Context(), store.JobFilter{
		TargetID: targetID,
		Status:   q.Get("status"),
		Limit:    limit,
		Offset:   offset,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询任务失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "非法 ID")
		return
	}
	job, err := s.Store.GetJob(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "任务不存在")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleJobLogs(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "非法 ID")
		return
	}
	lines := s.Logs.History(id)
	if len(lines) == 0 {
		// 进程重启后内存日志已丢，回退到磁盘文件
		if body, err := readJobLogFile(s.DataDir, id); err == nil {
			lines = strings.Split(strings.TrimRight(body, "\n"), "\n")
		}
	}
	if lines == nil {
		lines = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines})
}

func (s *Server) handleJobLogStream(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "非法 ID")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "当前服务器不支持流式输出")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ch, cancel := s.Logs.Subscribe(id)
	defer cancel()

	// 先通知前端清空已有日志，再回放历史，避免重连后出现重复行。
	fmt.Fprint(w, "event: reset\ndata: start\n\n")
	flusher.Flush()

	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case line, open := <-ch:
			if !open {
				return
			}
			for _, l := range strings.Split(line, "\n") {
				fmt.Fprintf(w, "data: %s\n\n", l)
			}
			flusher.Flush()
		}
	}
}

func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	if !s.requireWrite(w, r) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "非法 ID")
		return
	}
	job, err := s.Store.GetJob(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "任务不存在")
		return
	}
	if job.Status != domain.JobPending && job.Status != domain.JobRunning {
		writeErr(w, http.StatusConflict, "任务已结束，无法取消")
		return
	}
	if err := s.Store.CancelPendingJob(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, "取消排队任务失败")
		return
	}
	s.Runner.Cancel(id)
	s.audit(r, "job.cancel", "job:"+itoa(id), nil)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleJobArtifacts(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "非法 ID")
		return
	}
	arts, err := s.Store.ListArtifactsByJob(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询备份产物失败")
		return
	}
	writeJSON(w, http.StatusOK, arts)
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "非法 ID")
		return
	}
	a, err := s.Store.GetArtifact(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "备份不存在")
		return
	}
	if a.IsDeleted {
		writeErr(w, http.StatusGone, "备份已按保留策略清理")
		return
	}
	rc, err := s.Blob.Open(r.Context(), a.ObjectKey)
	if err != nil {
		if err == storage.ErrNotFound {
			writeErr(w, http.StatusNotFound, "存储中找不到该文件")
			return
		}
		writeErr(w, http.StatusInternalServerError, "读取备份失败")
		return
	}
	defer rc.Close()

	ext := ".dump"
	key := strings.TrimSuffix(a.ObjectKey, ".enc")
	if strings.HasSuffix(key, ".sql.gz") {
		ext = ".sql.gz"
	} else if strings.HasSuffix(key, ".sql") {
		ext = ".sql"
	}
	name := fmt.Sprintf("%s-job%d%s", sanitizeName(a.TargetName), a.JobID, ext)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(
		`attachment; filename="%s"; filename*=UTF-8''%s`, name, url.PathEscape(name)))
	// 加密备份输出的是解密后的明文，长度与密文不同，此时不能声明 Content-Length，
	// 否则客户端会认为传输不完整（curl 退出码 18）。
	if a.SizeBytes > 0 && !a.Encrypted {
		w.Header().Set("Content-Length", strconv.FormatInt(a.SizeBytes, 10))
	}
	w.WriteHeader(http.StatusOK)

	// 加密备份对外输出明文：传输由 HTTPS 保障，落盘才需要密文。
	if a.Encrypted {
		dr, derr := s.Keys.NewDecReader(rc)
		if derr != nil {
			slog.Error("解密备份失败", "artifact", a.ID, "err", derr)
			return
		}
		if _, err := io.Copy(w, dr); err != nil {
			slog.Warn("下载中断", "artifact", a.ID, "err", err)
		}
		return
	}
	if _, err := io.Copy(w, rc); err != nil {
		slog.Warn("下载中断", "artifact", a.ID, "err", err)
	}
}

func sanitizeName(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "backup"
	}
	return b.String()
}
