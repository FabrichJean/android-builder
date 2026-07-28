package server

import "net/http"

// currentUserID renvoie l'ID Google de la session en cours, ou "" si anonyme.
func (s *Server) currentUserID(r *http.Request) string {
	u, ok := s.auth.UserFromRequest(r)
	if !ok {
		return ""
	}
	return u.ID
}

// handleMe renvoie l'état de connexion courant (pour afficher le bouton
// connexion/déconnexion et l'avatar côté interface web).
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	if !s.auth.Enabled() {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "logged_in": false})
		return
	}
	u, ok := s.auth.UserFromRequest(r)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "logged_in": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": true, "logged_in": true, "user": u,
		// La génération d'app par IA n'est proposée qu'aux comptes connectés,
		// et seulement si le CLI claude est disponible sur le serveur.
		"gen": s.gen.Enabled(),
		// Compte banni de la génération IA (trop de demandes refusées) : le
		// reste du service (builds URL/projet, historique) reste utilisable.
		"gen_banned": s.gen.IsBanned(u.ID),
	})
}

func (s *Server) handleGoogleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.auth.Enabled() {
		writeErr(w, http.StatusNotFound, "connexion Google non configurée")
		return
	}
	url, err := s.auth.BeginLogin(w)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	http.Redirect(w, r, url, http.StatusFound)
}

func (s *Server) handleGoogleCallback(w http.ResponseWriter, r *http.Request) {
	if !s.auth.Enabled() {
		writeErr(w, http.StatusNotFound, "connexion Google non configurée")
		return
	}
	if !s.auth.CheckState(r) {
		writeErr(w, http.StatusBadRequest, "état OAuth invalide (CSRF ?)")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		writeErr(w, http.StatusBadRequest, "code d'autorisation manquant")
		return
	}
	u, err := s.auth.Exchange(r.Context(), code)
	if err != nil {
		s.log.Error("échange OAuth Google échoué", "err", err)
		writeErr(w, http.StatusBadGateway, "connexion Google impossible: "+err.Error())
		return
	}
	if err := s.auth.SetSession(w, u); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Retour direct dans le studio (la racine est la landing marketing).
	http.Redirect(w, r, "/app", http.StatusFound)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.auth.ClearSession(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
