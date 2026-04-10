package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Unmatched /api/* routes should return 404 with a body pointing agents at
// /contribute and / so they stop guessing route names blindly.
func TestMux_UnmatchedAPIRoute_ReturnsHint(t *testing.T) {
	h, cleanup := newTestHandler(t)
	defer cleanup()

	cases := []struct{ method, path string }{
		{"GET", "/api/frontier/status"},
		{"GET", "/api/crawl/stats"},
		{"GET", "/api/embed/queue"},
		{"POST", "/api/contribute/embed/queue"},
	}

	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.path, nil)
			rec := httptest.NewRecorder()
			h.Mux().ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rec.Code)
			}
			body := rec.Body.String()
			if !strings.Contains(body, "/contribute") {
				t.Errorf("body missing /contribute hint:\n%s", body)
			}
			if !strings.Contains(body, c.path) {
				t.Errorf("body missing attempted path %q:\n%s", c.path, body)
			}
		})
	}
}

// Registered routes must still win over the /api/ catch-all.
func TestMux_RegisteredRoutes_NotShadowedByCatchAll(t *testing.T) {
	h, cleanup := newTestHandler(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/api/stats", nil)
	rec := httptest.NewRecorder()
	h.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/stats status = %d, want 200 (catch-all shadowed real route)", rec.Code)
	}
}
