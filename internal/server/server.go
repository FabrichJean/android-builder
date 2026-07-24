// Package server expose l'API HTTP et orchestre le cycle de vie d'un build.
package server

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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
	"github.com/example/android-builder/internal/webui"
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
	mux.HandleFunc("GET /api/builds/{id}/events", s.handleEvents)
	mux.HandleFunc("GET /api/builds/{id}/apk", s.handleDownloadAPK)
	// Interface web statique (index.html embarqué) sur toutes les autres routes.
	mux.Handle("GET /", http.FileServerFS(webui.FS()))
	return mux
}

type createRequest struct {
	URL         string `json:"url"`
	AppName     string `json:"app_name"`
	Package     string `json:"package"`
	VersionName string `json:"version_name"`
	VersionCode string `json:"version_code"`
	SplashBg    string `json:"splash_bg"`
}

var (
	pkgRe   = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)
	urlRe   = regexp.MustCompile(`^https?://`)
	vnameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	vcodeRe = regexp.MustCompile(`^[0-9]+$`)
)

const (
	maxDistBytes    = 80 << 20 // 80 Mo : dist uploadé (mode bundle)
	maxIconBytes    = 3 << 20  // 3 Mo : icône PNG
	defaultSplashBg = "#14110b"
)

var splashRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// isPNG vérifie la signature magique d'un PNG.
func isPNG(b []byte) bool {
	return len(b) >= 8 && b[0] == 0x89 && b[1] == 'P' && b[2] == 'N' && b[3] == 'G'
}

// isZip vérifie la signature magique d'un fichier zip.
func isZip(b []byte) bool {
	return len(b) >= 4 && b[0] == 'P' && b[1] == 'K' && (b[2] == 3 || b[2] == 5 || b[2] == 7)
}

// zipHasIndexHTML confirme la présence d'un index.html dans l'archive.
func zipHasIndexHTML(b []byte) bool {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return false
	}
	for _, f := range zr.File {
		if strings.EqualFold(filepath.Base(f.Name), "index.html") {
			return true
		}
	}
	return false
}

// handleCreateBuild route selon le type de contenu :
//   - application/json          -> mode URL (WebView sur une URL distante)
//   - multipart/form-data       -> mode bundle (dist d'un projet web embarqué)
func (s *Server) handleCreateBuild(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		s.createBundleBuild(w, r)
		return
	}
	s.createURLBuild(w, r)
}

func (s *Server) createURLBuild(w http.ResponseWriter, r *http.Request) {
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
	// Package optionnel : déduit du nom de l'app s'il n'est pas fourni.
	if req.Package == "" {
		req.Package = derivePackage(req.AppName)
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
	splashBg := strings.TrimSpace(req.SplashBg)
	if splashBg == "" {
		splashBg = defaultSplashBg
	}
	if !splashRe.MatchString(splashBg) {
		writeErr(w, http.StatusBadRequest, "splash_bg invalide (attendu: #RRGGBB)")
		return
	}

	id := newID()
	build := &buildstore.Build{
		ID:       id,
		URL:      req.URL,
		AppName:  req.AppName,
		Package:  req.Package,
		Mode:     "url",
		SplashBg: splashBg,
		Status:   buildstore.StatusPending,
	}
	s.store.Create(build)

	inputs := map[string]string{
		"build_id":     id,
		"app_url":      req.URL,
		"app_name":     req.AppName,
		"package_id":   req.Package,
		"version_name": req.VersionName,
		"version_code": req.VersionCode,
		"bundle":       "false",
		"has_icon":     "false",
		"splash_bg":    splashBg,
	}

	// Heure du dispatch : sert à ne chercher que les runs créés ensuite.
	dispatchedAt := time.Now()
	if err := s.gh.Dispatch(r.Context(), s.cfg.Ref, inputs); err != nil {
		s.store.Update(id, func(b *buildstore.Build) {
			b.Status = buildstore.StatusFailed
			b.Error = "impossible de déclencher le workflow: " + err.Error()
		})
		writeErr(w, http.StatusBadGateway, "déclenchement du workflow impossible: "+err.Error())
		return
	}

	// Suivi asynchrone : détaché du contexte de la requête HTTP.
	go s.watch(id, "apk-"+id, dispatchedAt, nil)

	writeJSON(w, http.StatusAccepted, map[string]any{
		"id":     id,
		"status": buildstore.StatusPending,
	})
}

// createBundleBuild gère les requêtes multipart : mode URL ou bundle, avec
// éventuellement une icône PNG et une couleur de splash. Les assets binaires
// (dist zip, icône) sont relayés au workflow via une release GitHub temporaire.
func (s *Server) createBundleBuild(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxDistBytes); err != nil {
		writeErr(w, http.StatusBadRequest, "formulaire multipart invalide: "+err.Error())
		return
	}

	appName := strings.TrimSpace(r.FormValue("app_name"))
	pkg := strings.TrimSpace(r.FormValue("package"))
	vname := strings.TrimSpace(r.FormValue("version_name"))
	vcode := strings.TrimSpace(r.FormValue("version_code"))
	appURL := strings.TrimSpace(r.FormValue("url"))
	splashBg := strings.TrimSpace(r.FormValue("splash_bg"))

	if appName == "" {
		writeErr(w, http.StatusBadRequest, "app_name est obligatoire")
		return
	}
	if pkg == "" {
		pkg = derivePackage(appName)
	}
	if !pkgRe.MatchString(pkg) {
		writeErr(w, http.StatusBadRequest, "package invalide (ex: com.exemple.monapp)")
		return
	}
	if vname == "" {
		vname = "1.0"
	}
	if vcode == "" {
		vcode = "1"
	}
	if !vnameRe.MatchString(vname) {
		writeErr(w, http.StatusBadRequest, "version_name invalide (autorisé: lettres, chiffres, . _ -)")
		return
	}
	if !vcodeRe.MatchString(vcode) {
		writeErr(w, http.StatusBadRequest, "version_code invalide (entier positif attendu)")
		return
	}
	if splashBg == "" {
		splashBg = defaultSplashBg
	}
	if !splashRe.MatchString(splashBg) {
		writeErr(w, http.StatusBadRequest, "splash_bg invalide (attendu: #RRGGBB)")
		return
	}

	// Dist (optionnel) : détermine le mode (bundle si présent, sinon URL).
	var distData []byte
	var distName string
	if file, header, err := r.FormFile("dist"); err == nil {
		defer file.Close()
		distData, err = io.ReadAll(io.LimitReader(file, maxDistBytes+1))
		if err != nil {
			writeErr(w, http.StatusBadRequest, "lecture du dist impossible")
			return
		}
		if int64(len(distData)) > maxDistBytes {
			writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("dist trop volumineux (max %d Mo)", maxDistBytes>>20))
			return
		}
		if !isZip(distData) || !zipHasIndexHTML(distData) {
			writeErr(w, http.StatusBadRequest, "le dist doit être un .zip contenant index.html")
			return
		}
		distName = header.Filename
	}

	bundle := distData != nil
	if !bundle && !urlRe.MatchString(appURL) {
		writeErr(w, http.StatusBadRequest, "fournis une url (http/https) ou un dist (.zip)")
		return
	}

	// Icône (optionnelle) : PNG.
	var iconData []byte
	if file, _, err := r.FormFile("icon"); err == nil {
		defer file.Close()
		iconData, err = io.ReadAll(io.LimitReader(file, maxIconBytes+1))
		if err != nil {
			writeErr(w, http.StatusBadRequest, "lecture de l'icône impossible")
			return
		}
		if int64(len(iconData)) > maxIconBytes {
			writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("icône trop volumineuse (max %d Mo)", maxIconBytes>>20))
			return
		}
		if !isPNG(iconData) {
			writeErr(w, http.StatusBadRequest, "l'icône doit être un PNG")
			return
		}
	}

	id := newID()
	displayURL := appURL
	appAssetURL := appURL
	mode := "url"
	if bundle {
		mode = "bundle"
		displayURL = distName
		appAssetURL = "file:///android_asset/www/index.html"
	}
	build := &buildstore.Build{
		ID:       id,
		URL:      displayURL,
		AppName:  appName,
		Package:  pkg,
		Mode:     mode,
		SplashBg: splashBg,
		HasIcon:  iconData != nil,
		Status:   buildstore.StatusPending,
	}
	s.store.Create(build)

	// Relais des assets binaires via une release temporaire (si nécessaire).
	var cleanup func()
	if bundle || iconData != nil {
		tag := "assets-" + id
		releaseID, err := s.gh.CreateRelease(r.Context(), tag, "APK assets "+id)
		if err != nil {
			s.failNow(w, id, "création de la release temporaire impossible: "+err.Error())
			return
		}
		cleanup = func() { _ = s.gh.DeleteReleaseAndTag(context.Background(), releaseID, tag) }
		if bundle {
			if err := s.gh.UploadReleaseAsset(r.Context(), releaseID, "dist.zip", "application/zip", distData); err != nil {
				cleanup()
				s.failNow(w, id, "upload du dist impossible: "+err.Error())
				return
			}
		}
		if iconData != nil {
			if err := s.gh.UploadReleaseAsset(r.Context(), releaseID, "icon.png", "image/png", iconData); err != nil {
				cleanup()
				s.failNow(w, id, "upload de l'icône impossible: "+err.Error())
				return
			}
		}
	}

	inputs := map[string]string{
		"build_id":     id,
		"app_url":      appAssetURL,
		"app_name":     appName,
		"package_id":   pkg,
		"version_name": vname,
		"version_code": vcode,
		"bundle":       boolStr(bundle),
		"has_icon":     boolStr(iconData != nil),
		"splash_bg":    splashBg,
	}

	dispatchedAt := time.Now()
	if err := s.gh.Dispatch(r.Context(), s.cfg.Ref, inputs); err != nil {
		if cleanup != nil {
			cleanup()
		}
		s.failNow(w, id, "déclenchement du workflow impossible: "+err.Error())
		return
	}

	go s.watch(id, "apk-"+id, dispatchedAt, cleanup)

	writeJSON(w, http.StatusAccepted, map[string]any{
		"id":     id,
		"status": buildstore.StatusPending,
	})
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// failNow marque un build en échec et répond une erreur HTTP.
func (s *Server) failNow(w http.ResponseWriter, id, msg string) {
	s.store.Update(id, func(b *buildstore.Build) {
		b.Status = buildstore.StatusFailed
		b.Error = msg
	})
	writeErr(w, http.StatusBadGateway, msg)
}

// watch repère le run correspondant, suit son exécution puis récupère l'APK.
// cleanup (optionnel) est exécuté à la fin, quel que soit le résultat.
func (s *Server) watch(id, runName string, dispatchedAt time.Time, cleanup func()) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if cleanup != nil {
		defer cleanup()
	}

	log := s.log.With("build_id", id)

	// 1. Repérer le run déclenché (le run-name reprend le build_id).
	var run *ghclient.Run
	for {
		if ctx.Err() != nil {
			s.fail(id, "délai dépassé en attendant le démarrage du workflow")
			return
		}
		r, err := s.gh.FindRunByName(ctx, runName, dispatchedAt)
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

	// 2. Attendre la fin du run en suivant les étapes en direct.
	for run.Status != "completed" {
		select {
		case <-ctx.Done():
			s.fail(id, "délai dépassé pendant le build")
			return
		case <-time.After(4 * time.Second):
		}

		// Étapes détaillées (progression fine).
		if steps, err := s.gh.GetSteps(ctx, run.ID); err == nil {
			s.store.Update(id, func(b *buildstore.Build) {
				applySteps(b, steps)
			})
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
	s.store.Update(id, func(b *buildstore.Build) {
		b.Progress = 100
		b.CurrentStep = ""
	})
	s.store.SetAPKPath(id, path) // passe le statut à success en dernier
	log.Info("APK écrit", "chemin", path, "taille", len(apk))
}

// applySteps met à jour les étapes, la progression et l'étape courante d'un build.
func applySteps(b *buildstore.Build, steps []ghclient.Step) {
	if len(steps) == 0 {
		return
	}
	out := make([]buildstore.Step, 0, len(steps))
	completed, current := 0, ""
	for _, st := range steps {
		out = append(out, buildstore.Step{Name: st.Name, Status: st.Status, Conclusion: st.Conclusion})
		if st.Status == "completed" {
			completed++
		}
		if st.Status == "in_progress" && current == "" {
			current = st.Name
		}
	}
	b.Steps = out
	b.Progress = completed * 100 / len(steps)
	if current != "" {
		b.CurrentStep = current
	}
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

// handleEvents diffuse en SSE l'état d'un build (progression, étapes, statut)
// en temps réel, jusqu'à ce qu'il soit terminé ou que le client se déconnecte.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming non supporté")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	var last string
	send := func(b *buildstore.Build) {
		payload, _ := json.Marshal(b)
		if string(payload) == last {
			return
		}
		last = string(payload)
		fmt.Fprintf(w, "data: %s\n\n", payload)
		flusher.Flush()
	}

	for {
		b, ok := s.store.Get(id)
		if !ok {
			fmt.Fprint(w, `data: {"status":"failed","error":"build introuvable"}`+"\n\n")
			flusher.Flush()
			return
		}
		send(b)
		if b.Status == buildstore.StatusSuccess || b.Status == buildstore.StatusFailed {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(time.Second):
		}
	}
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

// derivePackage construit un applicationId valide à partir du nom de l'app,
// ex. "Mon Application !" -> "app.webview.monapplication".
func derivePackage(name string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		}
	}
	slug := sb.String()
	if len(slug) > 30 {
		slug = slug[:30]
	}
	// Un segment de package doit commencer par une lettre.
	if slug == "" || slug[0] < 'a' || slug[0] > 'z' {
		slug = "app" + slug
	}
	return "app.webview." + slug
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
