package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/example/android-builder/internal/buildstore"
)

func (s *Server) handleGetBuild(w http.ResponseWriter, r *http.Request) {
	b, ok := s.store.Get(r.PathValue("id"))
	if !ok || !s.buildVisible(r, b) {
		writeErr(w, http.StatusNotFound, "build introuvable")
		return
	}
	writeJSON(w, http.StatusOK, b)
}

// handleDeleteBuild masque définitivement un build (suppression douce) :
// la ligne et les fichiers (apk, source, miniature) restent sur le serveur,
// mais le build disparaît de l'historique et de tous les endpoints publics.
func (s *Server) handleDeleteBuild(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	b, ok := s.store.Get(id)
	if !ok || !s.buildVisible(r, b) {
		writeErr(w, http.StatusNotFound, "build introuvable")
		return
	}
	s.store.SoftDelete(id)
	w.WriteHeader(http.StatusNoContent)
}

// buildVisible détermine si l'appelant peut accéder à un build donné : un
// build anonyme (créé sans compte) reste accessible à qui connaît son ID
// (c'est son seul mécanisme d'accès côté client), mais un build rattaché à
// un compte ne doit être visible qu'à ce même compte — pas aux visiteurs
// anonymes, ni aux autres comptes.
func (s *Server) buildVisible(r *http.Request, b *buildstore.Build) bool {
	if b.Deleted {
		return false
	}
	return b.UserID == "" || b.UserID == s.currentUserID(r)
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

	if b, ok := s.store.Get(id); !ok || !s.buildVisible(r, b) {
		writeErr(w, http.StatusNotFound, "build introuvable")
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
	if !ok || !s.buildVisible(r, b) {
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

// handleDownloadSource sert le dist.zip source d'un build en mode "bundle"
// (projet importé manuellement ou généré par IA) — conservé sur disque à la
// création du build, indépendamment du statut/de la réussite du build APK.
func (s *Server) handleDownloadSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	b, ok := s.store.Get(id)
	if !ok || !s.buildVisible(r, b) {
		writeErr(w, http.StatusNotFound, "build introuvable")
		return
	}
	path, ok := s.store.SourcePath(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "code source indisponible")
		return
	}
	filename := fmt.Sprintf("%s-%s-source.zip", sanitize(b.AppName), id)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	http.ServeFile(w, r, path)
}

// handleListBuilds renvoie l'historique privé de l'utilisateur connecté.
// Anonyme -> liste vide (l'interface retombe sur son historique localStorage).
func (s *Server) handleListBuilds(w http.ResponseWriter, r *http.Request) {
	userID := s.currentUserID(r)
	if userID == "" {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	builds, err := s.store.List(userID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "lecture de l'historique impossible: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, builds)
}
