package app_test

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/app"
)

var testUI = fstest.MapFS{
	"index.html":         {Data: []byte("<!doctype html><title>t</title>")},
	"assets/app-abc.js":  {Data: []byte("console.log(1)")},
	"assets/app-abc.css": {Data: []byte("body{}")},
}

// uiEngine serves the two web UI routes from ui.
func uiEngine(t *testing.T, ui fs.FS) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctrl := app.NewController(realSvc(t), app.Options{UI: ui})
	e := gin.New()
	e.GET("/", ctrl.Index)
	e.GET("/assets/*filepath", ctrl.Asset)
	return e
}

const wantCSP = "default-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"

func TestIndex_ServesTheEntryPage(t *testing.T) {
	w := do(uiEngine(t, testUI), http.MethodGet, "/")
	require.Equal(t, 200, w.Code)
	assert.Equal(t, "<!doctype html><title>t</title>", w.Body.String())
	assert.Equal(t, "text/html; charset=utf-8", w.Header().Get("Content-Type"))
	assert.Equal(t, "no-cache", w.Header().Get("Cache-Control"))
	assert.Equal(t, wantCSP, w.Header().Get("Content-Security-Policy"))
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "no-referrer", w.Header().Get("Referrer-Policy"))
}

func TestIndex_NotBuilt(t *testing.T) {
	for name, ui := range map[string]fs.FS{"no ui": nil, "placeholder only": fstest.MapFS{".gitkeep": {}}} {
		t.Run(name, func(t *testing.T) {
			w := do(uiEngine(t, ui), http.MethodGet, "/")
			require.Equal(t, 404, w.Code)
			var body struct {
				Error struct{ Code string } `json:"error"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.Equal(t, "not_found", body.Error.Code)
		})
	}
}

func TestAsset_ServesHashedFilesForAYear(t *testing.T) {
	for _, tc := range []struct{ path, body, contentType string }{
		{"/assets/app-abc.js", "console.log(1)", "text/javascript"},
		{"/assets/app-abc.css", "body{}", "text/css"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := do(uiEngine(t, testUI), http.MethodGet, tc.path)
			require.Equal(t, 200, w.Code)
			assert.Equal(t, tc.body, w.Body.String())
			assert.True(t, strings.HasPrefix(w.Header().Get("Content-Type"), tc.contentType), w.Header().Get("Content-Type"))
			assert.Equal(t, "public, max-age=31536000, immutable", w.Header().Get("Cache-Control"))
			assert.Equal(t, wantCSP, w.Header().Get("Content-Security-Policy"))
			assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
		})
	}
}

// Anything under /assets that is not a file is 404: no directory listing, no
// entry page, and no long-lived cache header.
func TestAsset_NotAFile(t *testing.T) {
	paths := []string{
		"/assets/",
		"/assets/missing-123.js",
		"/assets/../index.html",
		"/assets/%2e%2e/index.html",
		"/assets/app-abc.js/",
	}
	for name, ui := range map[string]fs.FS{"built": testUI, "no ui": nil} {
		for _, p := range paths {
			t.Run(name+" "+p, func(t *testing.T) {
				w := do(uiEngine(t, ui), http.MethodGet, p)
				assert.Equal(t, 404, w.Code)
				assert.Empty(t, w.Header().Get("Cache-Control"))
				assert.NotContains(t, w.Body.String(), "<title>")
			})
		}
	}
}
