package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/yufei/feixi/internal/web"
)

func mountSPA(r chi.Router) {
	files, err := web.FS()
	if err != nil {
		r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			writeText(w, http.StatusServiceUnavailable, "前端资源不可用")
		})
		return
	}

	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		serveStatic(w, req, files)
	})
}

func serveStatic(w http.ResponseWriter, req *http.Request, files fs.FS) {
	name := strings.TrimPrefix(path.Clean(req.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}

	body, err := fs.ReadFile(files, name)
	if err != nil || len(body) == 0 {
		// 带扩展名的路径不是前端路由：回落成 index.html 会让浏览器把 HTML 当
		// manifest 或图标用，MIME 不合法、PWA 静默失效，且难排查。
		if ext := path.Ext(name); ext != "" && ext != ".html" {
			writeText(w, http.StatusNotFound, "资源不存在")
			return
		}
		// Unknown paths fall back to the SPA entry so client-side routing works.
		body, err = fs.ReadFile(files, "index.html")
		if err != nil {
			// dist/ 里只有占位的 .gitkeep：说明还没跑过前端构建。
			writeText(w, http.StatusServiceUnavailable, "前端尚未构建：运行 make web 后重新编译，或用 make dev 起开发服务器。")
			return
		}
		name = "index.html"
	}

	if strings.HasPrefix(name, "assets/") {
		// Vite names hashed bundles, so they can be cached forever.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	if name == "sw.js" {
		// 浏览器只在脚本字节变化时装新 SW，而发版改的是 hashed 产物、不是这个文件本身。
		// 用入口 HTML 的指纹顶掉占位符，脚本就跟着本次构建变，旧缓存才会在 activate 时被清掉。
		if shell, err := fs.ReadFile(files, "index.html"); err == nil {
			body = bytes.ReplaceAll(body, []byte("__BUILD_VERSION__"), []byte(buildFingerprint(shell)))
		}
	}
	w.Header().Set("Content-Type", contentType(name))
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

// buildFingerprint 是本次前端构建的身份：入口 HTML 里引用着 hashed 产物名，
// 所以它变了就意味着该换一份 Service Worker 缓存。
func buildFingerprint(shell []byte) string {
	sum := sha256.Sum256(shell)
	return hex.EncodeToString(sum[:])[:12]
}

func contentType(name string) string {
	switch {
	case strings.HasSuffix(name, ".html"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(name, ".js"):
		return "text/javascript; charset=utf-8"
	case strings.HasSuffix(name, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(name, ".webmanifest"):
		// Go 的 mime.TypeByExtension 不认 .webmanifest，浏览器却对 manifest 的
		// MIME 严格，只能显式给。
		return "application/manifest+json"
	case strings.HasSuffix(name, ".png"):
		return "image/png"
	case strings.HasSuffix(name, ".ico"):
		return "image/vnd.microsoft.icon"
	case strings.HasSuffix(name, ".json"):
		return "application/json"
	default:
		return "application/octet-stream"
	}
}
