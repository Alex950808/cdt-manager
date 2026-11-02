package main

import (
	"embed"
	"net/http"
	"strings"
)

//go:embed web/dist
var webFS embed.FS

var contentTypeByExt = map[string]string{
	".html": "text/html; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".js":   "text/javascript; charset=utf-8",
	".svg":         "image/svg+xml",
	".png":         "image/png",
	".webmanifest": "application/manifest+json",
	".json":        "application/json",
	".ico":         "image/x-icon",
}

// serveWeb 输出嵌入的静态文件。前端为单文件 SPA（CSS/JS 全部内联在 index.html）。
func serveWeb(w http.ResponseWriter, name string) {
	raw, err := webFS.ReadFile("web/" + name)
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	ext := name
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		ext = name[i:]
	}
	if ct, ok := contentTypeByExt[ext]; ok {
		w.Header().Set("Content-Type", ct)
	}
	// 无版本化资源：禁缓存防新旧混搭
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(raw)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	serveWeb(w, "dist/index.html")
}

func (s *Server) handleAsset(w http.ResponseWriter, r *http.Request) {
	serveWeb(w, "dist" + r.URL.Path) // /assets/xxx
}

func (s *Server) handlePublicFile(filename string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		serveWeb(w, "dist/"+filename)
	}
}
