// Package managerext adds the per-instance calling switch (KRIONIX) to the
// official Evolution GO Manager.
//
// The Manager ships prebuilt in manager/dist and its source is not in this
// repository, so its minified bundle is left untouched: index.html is served with
// one extra <script> tag, and that readable script (calling.js) renders a
// "Llamadas WhatsApp" card inside each instance's settings page. Replacing
// manager/dist with a newer upstream build keeps working.
package managerext

import (
	"bytes"
	_ "embed"
	"net/http"
	"os"
	"sync"

	"github.com/gin-gonic/gin"
)

// ScriptPath is where the extension is served. It sits under /manager so the
// Manager's own static handling (and its license exemption) applies to it.
const ScriptPath = "/manager/krionix-calling.js"

//go:embed calling.js
var script []byte

var scriptTag = []byte(`<script src="` + ScriptPath + `" defer></script>`)

// indexCache holds manager/dist/index.html with the script tag injected, rebuilt
// when the file changes on disk.
var indexCache struct {
	sync.Mutex
	modTime int64
	size    int64
	body    []byte
}

func injectedIndex(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	indexCache.Lock()
	defer indexCache.Unlock()
	if indexCache.body != nil && indexCache.modTime == info.ModTime().UnixNano() && indexCache.size == info.Size() {
		return indexCache.body, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	indexCache.body = inject(raw)
	indexCache.modTime, indexCache.size = info.ModTime().UnixNano(), info.Size()
	return indexCache.body, nil
}

// inject places the script tag before </head> (or appends it if there is none).
func inject(index []byte) []byte {
	if bytes.Contains(index, scriptTag) {
		return index
	}
	if i := bytes.Index(index, []byte("</head>")); i >= 0 {
		out := make([]byte, 0, len(index)+len(scriptTag)+4)
		out = append(out, index[:i]...)
		out = append(out, "  "...)
		out = append(out, scriptTag...)
		out = append(out, '\n')
		return append(out, index[i:]...)
	}
	return append(append([]byte{}, index...), scriptTag...)
}

// ServeManager serves the Manager SPA (index.html with the extension) and the
// extension script itself.
func ServeManager(indexPath string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path == ScriptPath {
			c.Header("Cache-Control", "no-cache")
			c.Data(http.StatusOK, "text/javascript; charset=utf-8", script)
			return
		}
		body, err := injectedIndex(indexPath)
		if err != nil {
			c.File(indexPath)
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", body)
	}
}
