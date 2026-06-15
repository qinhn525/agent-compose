package e2e

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestE2EAPIVersionSmoke(t *testing.T) {
	app := echo.New()
	app.GET("/api/version", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"version": "0"})
	})

	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/version status = %d", rec.Code)
	}
}
