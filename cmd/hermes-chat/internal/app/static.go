package app

import (
	"io/fs"
	"net/http"
	"path"

	"github.com/gin-gonic/gin"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
)

// contentSecurityPolicy is the whole policy sent with the web UI. The page
// loads nothing from another origin and runs no inline script; the script
// rules are not to be relaxed.
const contentSecurityPolicy = "default-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"

// assetCacheControl suits files whose names carry a hash of their content.
const assetCacheControl = "public, max-age=31536000, immutable"

// uiHeaders sets the security headers every web UI response carries.
func uiHeaders(c *gin.Context) {
	c.Header("Content-Security-Policy", contentSecurityPolicy)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "no-referrer")
}

// Index serves the web UI's entry page, which is never cached: it names the
// current hashed assets.
func (h *controller) Index(c *gin.Context) {
	if h.opts.UI == nil {
		writeError(c, bus.ErrNotFound)
		return
	}
	// Not http.ServeFileFS: it answers a path ending in index.html with a
	// redirect to "./".
	page, err := fs.ReadFile(h.opts.UI, "index.html")
	if err != nil {
		writeError(c, bus.ErrNotFound) // the binary was built without the UI
		return
	}
	uiHeaders(c)
	c.Header("Cache-Control", "no-cache")
	c.Data(http.StatusOK, "text/html; charset=utf-8", page)
}

// Asset serves one built file from the UI's assets directory.
func (h *controller) Asset(c *gin.Context) {
	p := c.Param("filepath") // with its leading slash
	if h.opts.UI == nil || path.Clean(p) != p {
		writeError(c, bus.ErrNotFound)
		return
	}
	name := "assets" + p
	info, err := fs.Stat(h.opts.UI, name)
	if err != nil || !info.Mode().IsRegular() {
		writeError(c, bus.ErrNotFound)
		return
	}
	uiHeaders(c)
	c.Header("Cache-Control", assetCacheControl)
	http.ServeFileFS(c.Writer, c.Request, h.opts.UI, name)
}
