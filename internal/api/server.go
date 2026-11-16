package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/weigong/dbbackups/internal/auth"
	"github.com/weigong/dbbackups/internal/backup"
	"github.com/weigong/dbbackups/internal/domain"
	"github.com/weigong/dbbackups/internal/pg"
	"github.com/weigong/dbbackups/internal/secrets"
	"github.com/weigong/dbbackups/internal/storage"
	"github.com/weigong/dbbackups/internal/store"
)

const Version = "0.1.0-m1"

const cookieName = "dbb_sid"

type ctxKey string

const userKey ctxKey = "user"

type Server struct {
	Store        *store.Store
	Keys         *secrets.Keyring
	Tools        *pg.Toolchain
	Runner       *backup.Runner
	Sched        *backup.Scheduler
	Logs         *backup.LogHub
	Blob         storage.Storage
	DataDir      string
	WebDir       string
	SecureCookie bool
	SessionTTL   time.Duration
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(s.logRequests)

	r.Route("/api", func(r chi.Router) {
		r.Get("/system/bootstrap", s.handleBootstrap)
		r.Post("/auth/setup", s.handleSetup)
		r.Post("/auth/login", s.handleLogin)

		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)

			r.Get("/auth/me", s.handleMe)
			r.Post("/auth/logout", s.handleLogout)

			r.Get("/stats", s.handleStats)
			r.Get("/stats/daily", s.handleDaily)
			r.Get("/system/info", s.handleSystemInfo)
			r.Get("/audit", s.handleAudit)

			r.Get("/targets", s.handleListTargets)
			r.Post("/targets", s.handleCreateTarget)
			r.Get("/targets/{id}", s.handleGetTarget)
			r.Put("/targets/{id}", s.handleUpdateTarget)
			r.Delete("/targets/{id}", s.handleDeleteTarget)
			r.Post("/targets/{id}/test", s.handleTestTarget)
			r.Post("/targets/{id}/backup", s.handleManualBackup)

			r.Get("/policies", s.handleListPolicies)
			r.Post("/policies", s.handleCreatePolicy)
			r.Put("/policies/{id}", s.handleUpdatePolicy)
			r.Delete("/policies/{id}", s.handleDeletePolicy)

			r.Get("/jobs", s.handleListJobs)
			r.Get("/jobs/{id}", s.handleGetJob)
			r.Get("/jobs/{id}/logs", s.handleJobLogs)
			r.Get("/jobs/{id}/logs/stream", s.handleJobLogStream)
			r.Post("/jobs/{id}/cancel", s.handleCancelJob)
			r.Get("/jobs/{id}/artifacts", s.handleJobArtifacts)
			r.Get("/artifacts/{id}/download", s.handleDownload)
		})
	})

	r.Get("/*", s.serveSPA)
	return r
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		if !strings.HasPrefix(r.URL.Path, "/assets/") {
			slog.Info("http",
				"method", r.Method, "path", r.URL.Path,
				"status", ww.Status(), "ms", time.Since(start).Milliseconds())
		}
	})
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := ""
		if c, err := r.Cookie(cookieName); err == nil {
			raw = c.Value
		}
		if raw == "" {
			if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
				raw = strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
			}
		}
		if raw == "" {
			writeErr(w, http.StatusUnauthorized, "未登录")
			return
		}
		u, err := s.Store.GetSessionUser(r.Context(), auth.HashToken(raw))
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "会话已失效，请重新登录")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, u)))
	})
}

func userFrom(r *http.Request) *domain.User {
	u, _ := r.Context().Value(userKey).(*domain.User)
	return u
}

// requireWrite 检查写权限：viewer 只读，operator 可执行备份，admin 可改配置。
func (s *Server) requireWrite(w http.ResponseWriter, r *http.Request) bool {
	u := userFrom(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "未登录")
		return false
	}
	if u.Role == domain.RoleViewer {
		writeErr(w, http.StatusForbidden, "只读账号无权执行该操作")
		return false
	}
	return true
}

func (s *Server) audit(r *http.Request, action, ref string, detail any) {
	u := userFrom(r)
	actor := "system"
	if u != nil {
		actor = u.Email
	}
	body, _ := json.Marshal(detail)
	s.Store.AddAudit(r.Context(), actor, action, ref, clientIP(r), body)
}

func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		return strings.TrimSpace(strings.Split(v, ",")[0])
	}
	return r.RemoteAddr
}

// ---------- 响应工具 ----------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

type errBody struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, errBody{Error: msg})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		msg := "请求体格式错误"
		if errors.Is(err, io.EOF) {
			msg = "请求体为空"
		}
		writeErr(w, http.StatusBadRequest, msg)
		return false
	}
	return true
}

// ---------- 认证 ----------

type bootstrapResp struct {
	NeedsSetup bool   `json:"needs_setup"`
	Version    string `json:"version"`
}

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.CountUsers(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取初始化状态失败")
		return
	}
	writeJSON(w, http.StatusOK, bootstrapResp{NeedsSetup: n == 0, Version: Version})
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (c *credentials) validate() error {
	c.Email = strings.TrimSpace(strings.ToLower(c.Email))
	if len(c.Email) < 3 || !strings.Contains(c.Email, "@") {
		return errors.New("请输入有效邮箱")
	}
	if len(c.Password) < 8 {
		return errors.New("密码至少 8 位")
	}
	return nil
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.CountUsers(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取初始化状态失败")
		return
	}
	if n > 0 {
		writeErr(w, http.StatusConflict, "系统已初始化")
		return
	}
	var req credentials
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := req.validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "生成密码摘要失败")
		return
	}
	u, err := s.Store.CreateUser(r.Context(), req.Email, hash, domain.RoleAdmin)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "创建管理员失败")
		return
	}
	if !s.startSession(w, r, u) {
		return
	}
	s.Store.AddAudit(r.Context(), u.Email, "auth.setup", "user:"+u.Email, clientIP(r), nil)
	writeJSON(w, http.StatusCreated, u)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req credentials
	if !decodeJSON(w, r, &req) {
		return
	}
	u, err := s.Store.GetUserByEmail(r.Context(), strings.TrimSpace(strings.ToLower(req.Email)))
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "邮箱或密码错误")
		return
	}
	ok, err := auth.VerifyPassword(u.PasswordHash, req.Password)
	if err != nil || !ok {
		writeErr(w, http.StatusUnauthorized, "邮箱或密码错误")
		return
	}
	if !s.startSession(w, r, u) {
		return
	}
	s.Store.AddAudit(r.Context(), u.Email, "auth.login", "user:"+u.Email, clientIP(r), nil)
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u *domain.User) bool {
	raw, hash, err := auth.NewSessionToken()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "生成会话失败")
		return false
	}
	ttl := s.SessionTTL
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}
	if err := s.Store.CreateSession(r.Context(), hash, u.ID,
		r.UserAgent(), clientIP(r), time.Now().Add(ttl)); err != nil {
		writeErr(w, http.StatusInternalServerError, "保存会话失败")
		return false
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    raw,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.SecureCookie,
		MaxAge:   int(ttl.Seconds()),
	})
	return true
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, userFrom(r))
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		_ = s.Store.DeleteSession(r.Context(), auth.HashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: "", Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: s.SecureCookie, MaxAge: -1,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---------- 静态资源 ----------

func (s *Server) serveSPA(w http.ResponseWriter, r *http.Request) {
	if s.WebDir == "" {
		writeErr(w, http.StatusNotFound, "前端未构建")
		return
	}
	clean := filepath.Clean("/" + r.URL.Path)
	target := filepath.Join(s.WebDir, filepath.FromSlash(clean))
	root, _ := filepath.Abs(s.WebDir)
	abs, _ := filepath.Abs(target)
	if abs != root && !strings.HasPrefix(abs, root+string(os.PathSeparator)) {
		http.NotFound(w, r)
		return
	}
	if st, err := os.Stat(abs); err == nil && !st.IsDir() {
		http.ServeFile(w, r, abs)
		return
	}
	index := filepath.Join(root, "index.html")
	if _, err := os.Stat(index); err != nil {
		writeErr(w, http.StatusNotFound, "前端未构建，请先执行 npm run build")
		return
	}
	http.ServeFile(w, r, index)
}
