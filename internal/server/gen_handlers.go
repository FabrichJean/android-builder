package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/example/android-builder/internal/appgen"
)

// requireUser garantit qu'un compte est connecté (la génération par IA est
// réservée aux sessions authentifiées). Renvoie l'ID utilisateur ou écrit 401.
func (s *Server) requireUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID := s.currentUserID(r)
	if userID == "" {
		writeErr(w, http.StatusUnauthorized, "connexion requise pour la génération d'app par IA")
		return "", false
	}
	return userID, true
}

// handleGenPrepare démarre une session : questions de clarification + estimation
// de tokens. Si l'estimation dépasse la limite, la session revient directement
// en "over_budget" avec une description allégée proposée.
func (s *Server) handleGenPrepare(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if !s.gen.Enabled() {
		writeErr(w, http.StatusNotFound, "génération d'app par IA indisponible sur ce serveur")
		return
	}
	var req struct {
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON invalide")
		return
	}
	sess, err := s.gen.Prepare(r.Context(), userID, req.Description)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

// handleGenAnswers reçoit les réponses aux questions et lance la génération.
func (s *Server) handleGenAnswers(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		Answers map[string]string `json:"answers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON invalide")
		return
	}
	if err := s.gen.StartGenerate(r.PathValue("id"), userID, req.Answers); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": appgen.StatusGenerating})
}

// handleGenEvents diffuse en SSE l'état de la génération (tokens consommés,
// statut) jusqu'à son terme.
func (s *Server) handleGenEvents(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	flusher, okF := w.(http.Flusher)
	if !okF {
		writeErr(w, http.StatusInternalServerError, "streaming non supporté")
		return
	}
	if _, found := s.gen.Get(id, userID); !found {
		writeErr(w, http.StatusNotFound, "session de génération introuvable")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	var last []byte
	for {
		sess, found := s.gen.Get(id, userID)
		if !found {
			fmt.Fprint(w, `data: {"status":"failed","error":"session expirée"}`+"\n\n")
			flusher.Flush()
			return
		}
		payload, _ := json.Marshal(sess)
		if !bytes.Equal(payload, last) {
			last = payload
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
		}
		switch sess.Status {
		case appgen.StatusSuccess, appgen.StatusFailed, appgen.StatusOverBudget:
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(time.Second):
		}
	}
}

// handleGenZip sert le dist.zip généré (index.html), à réinjecter dans le
// pipeline de build bundle existant côté client.
func (s *Server) handleGenZip(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	data, found := s.gen.Zip(r.PathValue("id"), userID)
	if !found {
		writeErr(w, http.StatusNotFound, "archive indisponible")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="dist.zip"`)
	w.Write(data)
}
