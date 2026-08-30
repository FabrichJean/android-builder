package appgen

import (
	"database/sql"
	"time"
)

// Crédits journaliers de génération IA : 5 crédits/jour, 1 crédit = 2000
// tokens de sortie (soit 10000 tokens/jour), par compte. La remise à zéro ne
// demande aucune tâche planifiée : la clé de la table inclut la date du
// jour, donc un nouveau jour repart simplement sur une ligne à zéro.
const (
	creditsPerDay    = 5
	tokensPerCredit  = 2000
	dailyTokenBudget = creditsPerDay * tokensPerCredit // 10000
)

const creditsSchema = `
CREATE TABLE IF NOT EXISTS appgen_credits (
	user_id     TEXT NOT NULL,
	day         TEXT NOT NULL,
	tokens_used INTEGER NOT NULL DEFAULT 0,
	updated_at  INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (user_id, day)
);
`

// ensureCreditsSchema crée la table de suivi des crédits si besoin.
// Best-effort : une erreur ici dégrade juste remainingTokens/addUsage en
// no-op (log au démarrage), sans empêcher le serveur de tourner.
func ensureCreditsSchema(db *sql.DB, warn func(msg string, args ...any)) {
	if db == nil {
		return
	}
	if _, err := db.Exec(creditsSchema); err != nil {
		warn("création de la table de suivi des crédits de génération impossible", "err", err)
	}
}

// today identifie le jour courant (UTC) pour le suivi des crédits.
func today() string { return time.Now().UTC().Format("2006-01-02") }

// remainingTokens renvoie le budget de tokens de génération encore
// disponible aujourd'hui pour ce compte (budget personnalisé par l'admin ou
// défaut global). Sans base (db nil) ou pour un utilisateur anonyme, le
// budget plein est renvoyé (pas de suivi possible).
func (m *Manager) remainingTokens(userID string) int {
	budget := m.dailyBudget(userID)
	if m.db == nil || userID == "" {
		return budget
	}
	var used int
	err := m.db.QueryRow(`SELECT tokens_used FROM appgen_credits WHERE user_id = ? AND day = ?`,
		userID, today()).Scan(&used)
	if err != nil {
		return budget // pas de ligne aujourd'hui = rien consommé
	}
	remaining := budget - used
	if remaining < 0 {
		remaining = 0
	}
	return remaining
}

// addUsage crédite des tokens consommés au compteur du jour courant.
func (m *Manager) addUsage(userID string, tokens int) {
	if m.db == nil || userID == "" || tokens <= 0 {
		return
	}
	now := time.Now().UnixMilli()
	if _, err := m.db.Exec(`
		INSERT INTO appgen_credits (user_id, day, tokens_used, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, day) DO UPDATE SET
			tokens_used = tokens_used + excluded.tokens_used,
			updated_at = excluded.updated_at
	`, userID, today(), tokens, now); err != nil {
		m.log.Warn("enregistrement de la consommation de crédits impossible", "user", userID, "err", err)
	}
}

// RemainingTokens renvoie le budget de tokens de génération encore
// disponible aujourd'hui pour ce compte (remise à zéro chaque jour).
func (m *Manager) RemainingTokens(userID string) int { return m.remainingTokens(userID) }

// DailyTokenBudget renvoie le budget quotidien total de tokens de génération
// de ce compte (personnalisé par l'admin ou défaut global).
func (m *Manager) DailyTokenBudget(userID string) int { return m.dailyBudget(userID) }

// CreditsPerDay renvoie le nombre de crédits quotidiens (1 crédit = 2000 tokens).
func (m *Manager) CreditsPerDay() int { return creditsPerDay }
