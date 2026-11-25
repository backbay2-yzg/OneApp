package service

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// IconServer serves cached application PNG icons mounted at /icons on Wails' asset server.
type IconServer struct {
	iconsDir string
}

// NewIconServer creates an IconServer reading from iconsDir.
func NewIconServer(iconsDir string) *IconServer {
	return &IconServer{iconsDir: iconsDir}
}

func (s *IconServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	reqPath := strings.TrimPrefix(r.URL.Path, "/")
	if reqPath == "" || strings.Contains(reqPath, "/") || strings.Contains(reqPath, "\\") {
		http.NotFound(w, r)
		return
	}
	name := filepath.Base(reqPath)
	if name == "." || name == ".." || !strings.HasSuffix(strings.ToLower(name), ".png") {
		http.NotFound(w, r)
		return
	}

	fullPath := filepath.Join(s.iconsDir, name)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(data)
	}
}
