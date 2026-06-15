package restful

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSPAFileServerHandlerServesAssetsAndFallsBackToIndex(t *testing.T) {
	testSPAFileServerHandlerServesAssetsAndFallsBackToIndex(t)
}

func testSPAFileServerHandlerServesAssetsAndFallsBackToIndex(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("index"), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "assets"), 0o755); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "assets", "app.js"), []byte("app"), 0o644); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	handler := SPAFileServerHandler(root, "/ui", "index.html")
	for _, tt := range []struct {
		name       string
		path       string
		wantStatus int
		wantBody   string
	}{
		{name: "root", path: "/ui/", wantStatus: http.StatusOK, wantBody: "index"},
		{name: "asset", path: "/ui/assets/app.js", wantStatus: http.StatusOK, wantBody: "app"},
		{name: "app route", path: "/ui/runs", wantStatus: http.StatusOK, wantBody: "index"},
		{name: "missing asset", path: "/ui/assets/missing.js", wantStatus: http.StatusNotFound},
		{name: "missing extension path", path: "/ui/missing.js", wantStatus: http.StatusNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()
			handler(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantBody != "" && rec.Body.String() != tt.wantBody {
				t.Fatalf("body = %q, want %q", rec.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestSPAFileServerHandlerRedirectsPrefixWithoutSlash(t *testing.T) {
	testSPAFileServerHandlerRedirectsPrefixWithoutSlash(t)
}

func testSPAFileServerHandlerRedirectsPrefixWithoutSlash(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("index"), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	rec := httptest.NewRecorder()
	SPAFileServerHandler(root, "/ui", "index.html")(rec, req)

	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMovedPermanently)
	}
	if got := rec.Header().Get("Location"); got != "/ui/" {
		t.Fatalf("Location = %q, want %q", got, "/ui/")
	}
}
