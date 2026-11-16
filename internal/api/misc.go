package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

func readJobLogFile(dataDir string, jobID int64) (string, error) {
	p := filepath.Join(dataDir, "logs", fmt.Sprintf("job-%d.log", jobID))
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	st, err := s.Store.Stats(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "统计失败")
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleDaily(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	list, err := s.Store.DailyStats(r.Context(), days)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "统计失败")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version":    Version,
		"toolchain":  s.Tools.List(),
		"storage":    s.Blob.Kind(),
		"data_dir":   s.DataDir,
		"encryption": "AES-256-GCM (分块)",
	})
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := s.Store.ListAudit(r.Context(), limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询审计日志失败")
		return
	}
	writeJSON(w, http.StatusOK, list)
}
