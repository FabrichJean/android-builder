// Package server expose l'API HTTP et orchestre le cycle de vie d'un build.
package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/example/android-builder/internal/appgen"
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
	gen    *appgen.Manager
	log    *slog.Logger
	chrome string        // binaire Chrome pour les miniatures ("" si absent)
	sem    chan struct{} // limite les captures Chrome concurrentes
}

func New(cfg *config.Config, gh *ghclient.Client, store *buildstore.Store, authMgr *auth.Manager, genMgr *appgen.Manager, log *slog.Logger) *Server {
	chrome := thumb.ChromePath()
	if chrome == "" {
		log.Warn("Chrome introuvable — miniatures désactivées (installe Chrome ou définis CHROME_PATH)")
	}
	return &Server{
		cfg: cfg, gh: gh, store: store, auth: authMgr, gen: genMgr, log: log,
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
	mux.HandleFunc("GET /api/builds/{id}/source", s.handleDownloadSource)
	mux.HandleFunc("GET /api/builds/{id}/thumb", s.handleThumb)
	mux.HandleFunc("GET /api/builds", s.handleListBuilds)
	mux.HandleFunc("GET /api/me", s.handleMe)
	// Génération d'app par IA (CLI claude du serveur) — comptes connectés uniquement.
	mux.HandleFunc("POST /api/generate", s.handleGenPrepare)
	mux.HandleFunc("POST /api/generate/{id}/answers", s.handleGenAnswers)
	mux.HandleFunc("GET /api/generate/{id}/events", s.handleGenEvents)
	mux.HandleFunc("GET /api/generate/{id}/dist.zip", s.handleGenZip)
	mux.HandleFunc("GET /auth/google/login", s.handleGoogleLogin)
	mux.HandleFunc("GET /auth/google/callback", s.handleGoogleCallback)
	mux.HandleFunc("POST /auth/logout", s.handleLogout)
	// Landing page marketing sur la racine exacte ; le studio vit sur /app
	// et /historique (même index.html, la vue est choisie via location.pathname).
	mux.HandleFunc("GET /{$}", s.handleLanding)
	mux.HandleFunc("GET /app", s.handleIndexPage)
	mux.HandleFunc("GET /historique", s.handleIndexPage)
	// Assets statiques (style.css, app.js, landing.css…) sur les autres routes.
	mux.Handle("GET /", http.FileServerFS(webui.FS()))
	return mux
}

func (s *Server) handleLanding(w http.ResponseWriter, r *http.Request) {
	http.ServeFileFS(w, r, webui.FS(), "landing.html")
}

// handleIndexPage sert la page du studio — /app et /historique sont de vraies
// URL navigables (rechargement, retour arrière) rendues par le même index.html.
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
