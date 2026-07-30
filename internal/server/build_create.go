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
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/example/android-builder/internal/buildstore"
)

type createRequest struct {
	URL              string `json:"url"`
	AppName          string `json:"app_name"`
	Package          string `json:"package"`
	VersionName      string `json:"version_name"`
	VersionCode      string `json:"version_code"`
	SplashBg         string `json:"splash_bg"`
	HideScrollbar    bool   `json:"hide_scrollbar"`
	DebugConsole     bool   `json:"debug_console"`
	FixImages        bool   `json:"fix_images"`
	BackgroundPlayer bool   `json:"background_player"`
	PictureInPicture bool   `json:"picture_in_picture"`
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
		UserID:   s.currentUserID(r),
		URL:      req.URL,
		AppName:  req.AppName,
		Package:  req.Package,
		Mode:     "url",
		SplashBg: splashBg,
		Status:   buildstore.StatusPending,
	}
	if err := s.store.Create(build); err != nil {
		writeErr(w, http.StatusInternalServerError, "enregistrement du build impossible: "+err.Error())
		return
	}

	inputs := map[string]string{
		"build_id":           id,
		"app_url":            req.URL,
		"app_name":           req.AppName,
		"package_id":         req.Package,
		"version_name":       req.VersionName,
		"version_code":       req.VersionCode,
		"bundle":             "false",
		"has_icon":           "false",
		"splash_bg":          splashBg,
		"hide_scrollbar":     boolStr(req.HideScrollbar),
		"debug_console":      boolStr(req.DebugConsole),
		"fix_images":         boolStr(req.FixImages),
		"background_player":  boolStr(req.BackgroundPlayer),
		"picture_in_picture": boolStr(req.PictureInPicture),
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
	go s.captureThumb(id, "url", req.URL, nil)

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
	hideScrollbar := r.FormValue("hide_scrollbar") == "true"
	debugConsole := r.FormValue("debug_console") == "true"
	fixImages := r.FormValue("fix_images") == "true"
	backgroundPlayer := r.FormValue("background_player") == "true"
	pictureInPicture := r.FormValue("picture_in_picture") == "true"

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
	var sourcePath string
	if bundle {
		mode = "bundle"
		displayURL = distName
		// Servi via WebViewAssetLoader (domaine virtuel https) : gère les chemins
		// absolus des SPA et permet le chargement des modules ES.
		appAssetURL = "https://appassets.androidplatform.net/index.html"
		// Conserve le dist original sur disque : contrairement à l'APK (produit
		// par le workflow), c'est le seul endroit où ce code source persiste au-delà
		// de la release GitHub temporaire (supprimée après le build) — permet de
		// le retélécharger plus tard (notamment pour les projets générés par IA).
		sourcePath = filepath.Join(s.cfg.Releases, "sources", id+".zip")
		if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
			writeErr(w, http.StatusInternalServerError, "écriture du code source impossible: "+err.Error())
			return
		}
		if err := os.WriteFile(sourcePath, distData, 0o644); err != nil {
			writeErr(w, http.StatusInternalServerError, "écriture du code source impossible: "+err.Error())
			return
		}
	}
	build := &buildstore.Build{
		ID:         id,
		UserID:     s.currentUserID(r),
		URL:        displayURL,
		AppName:    appName,
		Package:    pkg,
		Mode:       mode,
		SplashBg:   splashBg,
		HasIcon:    iconData != nil,
		SourcePath: sourcePath,
		Status:     buildstore.StatusPending,
	}
	if err := s.store.Create(build); err != nil {
		writeErr(w, http.StatusInternalServerError, "enregistrement du build impossible: "+err.Error())
		return
	}

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
		"build_id":           id,
		"app_url":            appAssetURL,
		"app_name":           appName,
		"package_id":         pkg,
		"version_name":       vname,
		"version_code":       vcode,
		"bundle":             boolStr(bundle),
		"has_icon":           boolStr(iconData != nil),
		"splash_bg":          splashBg,
		"hide_scrollbar":     boolStr(hideScrollbar),
		"debug_console":      boolStr(debugConsole),
		"fix_images":         boolStr(fixImages),
		"background_player":  boolStr(backgroundPlayer),
		"picture_in_picture": boolStr(pictureInPicture),
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
	if bundle {
		go s.captureThumb(id, "bundle", "", distData)
	} else {
		go s.captureThumb(id, "url", appURL, nil)
	}

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
