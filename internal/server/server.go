// Package server expose l'API HTTP et orchestre le cycle de vie d'un build.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/example/android-builder/internal/buildstore"
	"github.com/example/android-builder/internal/config"
	"github.com/example/android-builder/internal/ghclient"
)

// Server relie l'API HTTP, le store et le client GitHub.
type Server struct {
	cfg   *config.Config
	gh    *ghclient.Client
	store *buildstore.Store
	log   *slog.Logger
}

func New(cfg *config.Config, gh *ghclient.Client, store *buildstore.Store, log *slog.Logger) *Server {
	return &Server{cfg: cfg, gh: gh, store: store, log: log}
}

// Routes construit le routeur HTTP.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("POST /api/builds", s.handleCreateBuild)
	mux.HandleFunc("GET /api/builds/{id}", s.handleGetBuild)
	mux.HandleFunc("GET /api/builds/{id}/apk", s.handleDownloadAPK)
	return mux
}

type createRequest struct {
	URL         string `json:"url"`
	AppName     string `json:"app_name"`
	Package     string `json:"package"`
	VersionName string `json:"version_name"`
	VersionCode string `json:"version_code"`
}

var (
	pkgRe   = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)
	urlRe   = regexp.MustCompile(`^https?://`)
	vnameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	vcodeRe = regexp.MustCompile(`^[0-9]+$`)
)

func (s *Server) handleCreateBuild(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON invalide")
		return
	}
	req.URL = strings.TrimSpace(req.URL)
	req.AppName = strings.TrimSpace(req.AppName)
	req.Package = strings.TrimSpace(req.Package)

	if !urlRe.MatchString(req.URL) {
		writeErr(w, http.StatusBadRequest, "url invalide (doit commencer par http:// ou https://)")
		return
	}
	if req.AppName == "" {
		writeErr(w, http.StatusBadRequest, "app_name est obligatoire")
		return
	}
	if !pkgRe.MatchString(req.Package) {
		writeErr(w, http.StatusBadRequest, "package invalide (ex: com.exemple.monapp)")
		return
	}
	if req.VersionName == "" {
		req.VersionName = "1.0"
	}
	if req.VersionCode == "" {
		req.VersionCode = "1"
	}
	if !vnameRe.MatchString(req.VersionName) {
		writeErr(w, http.StatusBadRequest, "version_name invalide (autorisé: lettres, chiffres, . _ -)")
		return
	}
	if !vcodeRe.MatchString(req.VersionCode) {
		writeErr(w, http.StatusBadRequest, "version_code invalide (entier positif attendu)")
		return
	}

	id := newID()
	build := &buildstore.Build{
		ID:      id,
		URL:     req.URL,
		AppName: req.AppName,
		Package: req.Package,
		Status:  buildstore.StatusPending,
	}
	s.store.Create(build)

	runName := "apk-" + id
	inputs := map[string]string{
		"build_id":     id,
		"app_url":      req.URL,
		"app_name":     req.AppName,
		"package_id":   req.Package,
		"version_name": req.VersionName,
		"version_code": req.VersionCode,
	}

	if err := s.gh.Dispatch(r.Context(), s.cfg.Ref, inputs); err != nil {
		s.store.Update(id, func(b *buildstore.Build) {
			b.Status = buildstore.StatusFailed
			b.Error = "impossible de déclencher le workflow: " + err.Error()
		})
		writeErr(w, http.StatusBadGateway, "déclenchement du workflow impossible: "+err.Error())
		return
	}

	// Suivi asynchrone : détaché du contexte de la requête HTTP.
	go s.watch(id, runName)

	writeJSON(w, http.StatusAccepted, map[string]any{
		"id":     id,
		"status": buildstore.StatusPending,
	})
}

// watch repère le run correspondant, suit son exécution puis récupère l'APK.
func (s *Server) watch(id, runName string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	log := s.log.With("build_id", id)

	// 1. Repérer le run déclenché (le run-name reprend le build_id).
	var run *ghclient.Run
	for {
		if ctx.Err() != nil {
			s.fail(id, "délai dépassé en attendant le démarrage du workflow")
			return
		}
		r, err := s.gh.FindRunByName(ctx, runName)
		if err != nil {
			log.Warn("find run", "err", err)
		} else if r != nil {
			run = r
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
	}

	s.store.Update(id, func(b *buildstore.Build) {
		b.Status = buildstore.StatusBuilding
		b.RunID = run.ID
		b.RunURL = run.HTMLURL
	})
	log.Info("run repéré", "run_id", run.ID, "url", run.HTMLURL)

	// 2. Attendre la fin du run.
	for run.Status != "completed" {
		select {
		case <-ctx.Done():
			s.fail(id, "délai dépassé pendant le build")
			return
		case <-time.After(10 * time.Second):
		}
		r, err := s.gh.GetRun(ctx, run.ID)
		if err != nil {
			log.Warn("get run", "err", err)
			continue
		}
		run = r
	}

	if run.Conclusion != "success" {
		s.fail(id, "build en échec (conclusion: "+run.Conclusion+"), voir "+run.HTMLURL)
		return
	}

	// 3. Récupérer l'APK depuis l'artifact.
	apk, err := s.gh.DownloadAPK(ctx, run.ID)
	if err != nil {
		s.fail(id, "récupération de l'APK impossible: "+err.Error())
		return
	}

	// 4. Écrire l'APK dans le dossier releases/.
	b, _ := s.store.Get(id)
	filename := fmt.Sprintf("%s-%s.apk", sanitize(b.AppName), id)
	path := filepath.Join(s.cfg.Releases, filename)
	if err := os.WriteFile(path, apk, 0o644); err != nil {
		s.fail(id, "écriture de l'APK impossible: "+err.Error())
		return
	}
	s.store.SetAPKPath(id, path)
	log.Info("APK écrit", "chemin", path, "taille", len(apk))
}

func (s *Server) fail(id, msg string) {
	s.log.Error("build échoué", "build_id", id, "msg", msg)
	s.store.Update(id, func(b *buildstore.Build) {
		b.Status = buildstore.StatusFailed
		b.Error = msg
	})
}

func (s *Server) handleGetBuild(w http.ResponseWriter, r *http.Request) {
	b, ok := s.store.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "build introuvable")
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (s *Server) handleDownloadAPK(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	b, ok := s.store.Get(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "build introuvable")
		return
	}
	if b.Status != buildstore.StatusSuccess {
		writeErr(w, http.StatusConflict, "APK pas encore prêt (statut: "+string(b.Status)+")")
		return
	}
	path, ok := s.store.APKPath(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "APK indisponible")
		return
	}
	filename := fmt.Sprintf("%s-%s.apk", sanitize(b.AppName), id)
	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	http.ServeFile(w, r, path)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func sanitize(s string) string {
	s = strings.ToLower(s)
	var out strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			out.WriteRune(r)
		} else {
			out.WriteRune('-')
		}
	}
	res := strings.Trim(out.String(), "-")
	if res == "" {
		return "app"
	}
	return res
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
