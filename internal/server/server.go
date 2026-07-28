// Package server expose l'API HTTP et orchestre le cycle de vie d'un build.
package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/example/android-builder/internal/auth"
	"github.com/example/android-builder/internal/buildstore"
	"github.com/example/android-builder/internal/config"
	"github.com/example/android-builder/internal/ghclient"
	"github.com/example/android-builder/internal/thumb"
	"github.com/example/android-builder/internal/webui"
)

// Server relie l'API HTTP, le store et le client GitHub.
type Server struct {
	cfg    *config.Config
	gh     *ghclient.Client
	store  *buildstore.Store
	auth   *auth.Manager
	log    *slog.Logger
	chrome string        // binaire Chrome pour les miniatures ("" si absent)
	sem    chan struct{} // limite les captures Chrome concurrentes
}

func New(cfg *config.Config, gh *ghclient.Client, store *buildstore.Store, authMgr *auth.Manager, log *slog.Logger) *Server {
	chrome := thumb.ChromePath()
	if chrome == "" {
		log.Warn("Chrome introuvable — miniatures désactivées (installe Chrome ou définis CHROME_PATH)")
	}
	return &Server{
		cfg: cfg, gh: gh, store: store, auth: authMgr, log: log,
		chrome: chrome,
		sem:    make(chan struct{}, 2),
	}
}

// Routes construit le routeur HTTP.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("POST /api/builds", s.handleCreateBuild)
	mux.HandleFunc("GET /api/builds/{id}", s.handleGetBuild)
	mux.HandleFunc("GET /api/builds/{id}/events", s.handleEvents)
	mux.HandleFunc("GET /api/builds/{id}/apk", s.handleDownloadAPK)
	mux.HandleFunc("GET /api/builds/{id}/thumb", s.handleThumb)
	mux.HandleFunc("GET /api/builds", s.handleListBuilds)
	mux.HandleFunc("GET /api/me", s.handleMe)
	mux.HandleFunc("GET /auth/google/login", s.handleGoogleLogin)
	mux.HandleFunc("GET /auth/google/callback", s.handleGoogleCallback)
	mux.HandleFunc("POST /auth/logout", s.handleLogout)
	// Vraie route "/historique" (au lieu d'un simple bascule JS) : rechargement,
	// retour arrière et lien direct pointent vers la même page index.html, qui
	// lit ensuite location.pathname pour afficher la bonne vue.
	mux.HandleFunc("GET /historique", s.handleIndexPage)
	// Interface web statique (index.html embarqué) sur toutes les autres routes.
	mux.Handle("GET /", http.FileServerFS(webui.FS()))
	return mux
}

// handleIndexPage sert la même page index.html que "/" — utilisé pour que
// "/historique" soit une vraie URL navigable (rechargement, retour arrière).
func (s *Server) handleIndexPage(w http.ResponseWriter, r *http.Request) {
	http.ServeFileFS(w, r, webui.FS(), "index.html")
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
