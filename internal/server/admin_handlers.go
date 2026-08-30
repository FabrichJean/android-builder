package server

import (
	"encoding/json"
	"net/http"
)

// requireAdmin renvoie l'utilisateur connecté s'il est administrateur
// (email listé dans ADMIN_EMAILS), sinon écrit l'erreur HTTP et renvoie false.
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	u, ok := s.auth.UserFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "connexion requise")
		return false
	}
	if !s.cfg.IsAdminEmail(u.Email) {
		writeErr(w, http.StatusForbidden, "accès réservé aux administrateurs")
		return false
	}
	return true
}

// handleAdminUsers liste les comptes connus avec leur consommation du jour,
// leur budget journalier (personnalisé ou défaut) et leur état de ban.
func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	users, err := s.users.List()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "lecture des comptes impossible: "+err.Error())
		return
	}
	usage := s.gen.UsageToday()
	type row struct {
		ID           string `json:"id"`
		Email        string `json:"email"`
		Name         string `json:"name"`
		Picture      string `json:"picture"`
		LastLogin    int64  `json:"last_login"`
		TokensUsed   int    `json:"tokens_used"`
		DailyBudget  int    `json:"daily_budget"`
		CustomBudget int    `json:"custom_budget"` // 0 = défaut global
		Banned       bool   `json:"banned"`
		Admin        bool   `json:"admin"`
	}
	out := make([]row, 0, len(users))
	for _, u := range users {
		out = append(out, row{
			ID: u.ID, Email: u.Email, Name: u.Name, Picture: u.Picture, LastLogin: u.LastLogin,
			TokensUsed:   usage[u.ID],
			DailyBudget:  s.gen.DailyTokenBudget(u.ID),
			CustomBudget: s.gen.UserBudget(u.ID),
			Banned:       s.gen.IsBanned(u.ID),
			Admin:        s.cfg.IsAdminEmail(u.Email),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"users":          out,
		"default_budget": s.gen.DailyTokenBudget(""),
	})
}

// handleAdminSetBudget fixe le budget journalier personnalisé d'un compte.
// Corps : {"daily_tokens": N} — N <= 0 revient au budget par défaut.
func (s *Server) handleAdminSetBudget(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	userID := r.PathValue("id")
	var body struct {
		DailyTokens int `json:"daily_tokens"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "corps JSON invalide")
		return
	}
	if err := s.gen.SetUserBudget(userID, body.DailyTokens); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"daily_budget": s.gen.DailyTokenBudget(userID),
	})
}

// handleAdminResetUser réinitialise les crédits du jour d'un compte.
func (s *Server) handleAdminResetUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	userID := r.PathValue("id")
	used, err := s.gen.ResetCredits(userID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "tokens_restored": used})
}

// handleAdminRejections liste les prompts refusés d'un compte (filtre
// sécurité/pertinence) — sert à comprendre un bannissement avant de décider
// de le lever.
func (s *Server) handleAdminRejections(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	userID := r.PathValue("id")
	rejections, err := s.gen.Rejections(userID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rejections": rejections})
}

// handleAdminUnban lève le bannissement de génération IA d'un compte.
func (s *Server) handleAdminUnban(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	userID := r.PathValue("id")
	if err := s.gen.Unban(userID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAdminResetAll réinitialise les crédits du jour de tous les comptes.
func (s *Server) handleAdminResetAll(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	n, err := s.gen.ResetAllCredits()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "users_reset": n})
}
