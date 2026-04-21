package main

import (
	"embed"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
)

//go:generate sh -c "find static -type f \\( -name '*.css' -o -name '*.js' \\) ! -name '*.gz' -exec gzip -kf {} \\;"

//go:embed all:static
var staticFiles embed.FS

// staticHandler returns an http.Handler that serves embedded static files.
// When a request includes Accept-Encoding: gzip and a pre-compressed .gz
// variant exists, the compressed version is served directly.
func staticHandler() http.Handler {
	sub, _ := fs.Sub(staticFiles, "static")
	return &gzipFileServer{fs: sub}
}

type gzipFileServer struct {
	fs fs.FS
}

func (s *gzipFileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/")
	if p == "" {
		http.NotFound(w, r)
		return
	}

	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		gzPath := p + ".gz"
		if f, err := s.fs.Open(gzPath); err == nil {
			defer f.Close()
			w.Header().Set("Content-Encoding", "gzip")
			w.Header().Set("Content-Type", contentType(p))
			w.Header().Set("Vary", "Accept-Encoding")
			io.Copy(w, f.(io.Reader))
			return
		}
	}

	w.Header().Set("Vary", "Accept-Encoding")
	http.ServeFileFS(w, r, s.fs, p)
}

func contentType(name string) string {
	ext := filepath.Ext(name)
	if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	return "application/octet-stream"
}
