package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/weigong/dbbackups/internal/domain"
)

func exploreHTTP(role, origin, contentType, body string) *http.Request {
	r := httptest.NewRequest("POST", "http://localhost/api/targets/1/explore", strings.NewReader(body))
	r.Header.Set("Content-Type", contentType)
	r.Header.Set("Origin", origin)
	ctx := context.WithValue(r.Context(), userKey, &domain.User{ID: 1, Role: role})
	route := chi.NewRouteContext()
	route.URLParams.Add("id", "1")
	return r.WithContext(context.WithValue(ctx, chi.RouteCtxKey, route))
}

func TestExplorerRequestGuards(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		for _, tc := range []struct {
			name, role, origin, contentType string
			status                          int
		}{
			{"viewer", "viewer", "", "application/json", 403},
			{"cross-site", "admin", "https://other.example", "application/json", 403},
			{"not-json", "admin", "", "text/plain", 415},
		} {
			t.Run(tc.name+map[bool]string{false: "-query", true: "-cancel"}[cancel], func(t *testing.T) {
				s := &Server{}
				w := httptest.NewRecorder()
				r := exploreHTTP(tc.role, tc.origin, tc.contentType, `{"request_id":"test-request","action":"tables"}`)
				if cancel {
					s.handleCancelExplore(w, r)
				} else {
					s.handleExplore(w, r)
				}
				if w.Code != tc.status {
					t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
				}
			})
		}
	}
}

func TestExplorerConcurrencyGuards(t *testing.T) {
	for _, queries := range []map[int64]exploration{
		{1: {RequestID: "running"}},
		{2: {}, 3: {}, 4: {}, 5: {}},
	} {
		s := &Server{queries: queries}
		w := httptest.NewRecorder()
		s.handleExplore(w, exploreHTTP("operator", "", "application/json", `{"request_id":"test-request","action":"tables"}`))
		if w.Code != 429 {
			t.Fatalf("status=%d", w.Code)
		}
	}
}

func TestExplorerCancelCannotTargetAnotherUserOrRequest(t *testing.T) {
	for _, queries := range []map[int64]exploration{
		{2: {RequestID: "test-request", TargetID: 1, Cancel: func() { t.Fatal("canceled another user") }}},
		{1: {RequestID: "other-request", TargetID: 1, Cancel: func() { t.Fatal("canceled another request") }}},
		{1: {RequestID: "test-request", TargetID: 2, Cancel: func() { t.Fatal("canceled another target") }}},
	} {
		s := &Server{queries: queries}
		w := httptest.NewRecorder()
		s.handleCancelExplore(w, exploreHTTP("operator", "", "application/json", `{"request_id":"test-request"}`))
		if w.Code != 409 {
			t.Fatalf("status=%d", w.Code)
		}
	}
}
