package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/example/android-builder/internal/thumb"
)

var idRe = regexp.MustCompile(`^[a-f0-9]{4,}$`)

// captureThumb génère (en arrière-plan) la miniature d'un build via Chrome local.
func (s *Server) captureThumb(id, mode, url string, dist []byte) {
	if s.chrome == "" {
		return
	}
	s.sem <- struct{}{}
	defer func() { <-s.sem }()

	out := filepath.Join(s.cfg.Thumbs, id+".png")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var err error
	if mode == "bundle" {
		err = thumb.CaptureDist(ctx, s.chrome, dist, out)
	} else {
		err = thumb.CaptureURL(ctx, s.chrome, url, out)
	}
	if err != nil {
		s.log.Warn("miniature", "build_id", id, "err", err)
	} else {
		s.log.Info("miniature générée", "build_id", id)
	}
}

// handleThumb sert la miniature PNG d'un build.
func (s *Server) handleThumb(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !idRe.MatchString(id) {
		writeErr(w, http.StatusBadRequest, "id invalide")
		return
	}
	if b, ok := s.store.Get(id); ok && !s.buildVisible(r, b) {
		writeErr(w, http.StatusNotFound, "miniature indisponible")
		return
	}
	path := filepath.Join(s.cfg.Thumbs, id+".png")
	if _, err := os.Stat(path); err != nil {
		writeErr(w, http.StatusNotFound, "miniature indisponible")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	// La miniature est générée une seule fois et ne change plus -> cache immuable.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFile(w, r, path)
}
