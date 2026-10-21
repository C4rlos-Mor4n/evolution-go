// Package calling_ui serves the per-instance calling admin page (KRIONIX).
//
// The official Manager is shipped prebuilt (manager/dist) and its source is not in
// this repository, so the switch lives on its own readable page instead of being
// patched into minified JavaScript. The page is static; every action goes through
// the /calling API with the global API key typed by the operator.
package calling_ui

import (
	_ "embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

//go:embed index.html
var page []byte

// Handler serves the admin page.
func Handler(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("X-Frame-Options", "DENY")
	c.Header("Content-Security-Policy", "default-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'")
	c.Data(http.StatusOK, "text/html; charset=utf-8", page)
}
