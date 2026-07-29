package appgen

import (
	"database/sql"
	"time"
)

// maxRejections est le nombre de prompts refusés (filtre sécurité/pertinence
// du prompt parser) tolérés avant bannissement du compte. Le bannissement ne
// s'applique qu'à la génération d'app par IA — le reste du service (build
// via URL ou projet importé, historique) reste utilisable normalement.
const maxRejections = 2

const abuseSchema = `
CREATE TABLE IF NOT EXISTS appgen_abuse (
	user_id        TEXT PRIMARY KEY,
	rejected_count INTEGER NOT NULL DEFAULT 0,
	banned         INTEGER NOT NULL DEFAULT 0,
	updated_at     INTEGER NOT NULL DEFAULT 0
);
`

// IsBanned indique si ce compte est banni de la génération d'app par IA.
// Sans base (db nil, ex. en test) ou pour un utilisateur anonyme, jamais banni.
func (m *Manager) IsBanned(userID string) bool {
	if m.db == nil || userID == "" {
		return false
	}
	var banned int
	if err := m.db.QueryRow(`SELECT banned FROM appgen_abuse WHERE user_id = ?`, userID).Scan(&banned); err != nil {
		return false // pas de ligne = jamais refusé = pas banni
	}
	return banned != 0
}

// recordRejection incrémente le compteur de prompts refusés pour ce compte et
// le bannit si la limite est atteinte. Renvoie true si le compte vient de
// passer banni (utile pour informer immédiatement l'utilisateur).
func (m *Manager) recordRejection(userID string) bool {
	if m.db == nil || userID == "" {
		return false
	}
	now := time.Now().UnixMilli()
	if _, err := m.db.Exec(`
		INSERT INTO appgen_abuse (user_id, rejected_count, banned, updated_at)
		VALUES (?, 1, 0, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			rejected_count = rejected_count + 1,
			updated_at = excluded.updated_at
	`, userID, now); err != nil {
		m.log.Warn("enregistrement du refus de génération impossible", "user", userID, "err", err)
		return false
	}
	var count int
	if err := m.db.QueryRow(`SELECT rejected_count FROM appgen_abuse WHERE user_id = ?`, userID).Scan(&count); err != nil {
		return false
	}
	if count >= maxRejections {
		if _, err := m.db.Exec(`UPDATE appgen_abuse SET banned = 1 WHERE user_id = ?`, userID); err != nil {
			m.log.Warn("bannissement impossible", "user", userID, "err", err)
			return false
		}
		m.log.Warn("compte banni de la génération d'app par IA", "user", userID, "rejected_count", count)
		return true
	}
	return false
}

// ensureAbuseSchema crée la table de suivi des abus si besoin. Best-effort :
// une erreur ici dégrade juste IsBanned/recordRejection en no-op (log au
// démarrage), sans empêcher le serveur de tourner.
func ensureAbuseSchema(db *sql.DB, warn func(msg string, args ...any)) {
	if db == nil {
		return
	}
	if _, err := db.Exec(abuseSchema); err != nil {
		warn("création de la table de suivi des abus de génération impossible", "err", err)
	}
}
