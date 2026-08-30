package appgen

import (
	"database/sql"
	"fmt"
	"time"
)

// Budgets personnalisés par utilisateur : l'admin peut remplacer le budget
// journalier par défaut (dailyTokenBudget) pour un compte donné. Une ligne
// absente = budget par défaut ; supprimer la ligne revient au défaut.
const budgetsSchema = `
CREATE TABLE IF NOT EXISTS appgen_budgets (
	user_id      TEXT PRIMARY KEY,
	daily_tokens INTEGER NOT NULL,
	updated_at   INTEGER NOT NULL DEFAULT 0
);
`

func ensureBudgetsSchema(db *sql.DB, warn func(msg string, args ...any)) {
	if db == nil {
		return
	}
	if _, err := db.Exec(budgetsSchema); err != nil {
		warn("création de la table des budgets personnalisés impossible", "err", err)
	}
}

// dailyBudget renvoie le budget journalier de ce compte : le budget
// personnalisé posé par un admin s'il existe, sinon le défaut global.
func (m *Manager) dailyBudget(userID string) int {
	if m.db == nil || userID == "" {
		return dailyTokenBudget
	}
	var custom int
	err := m.db.QueryRow(`SELECT daily_tokens FROM appgen_budgets WHERE user_id = ?`, userID).Scan(&custom)
	if err != nil || custom <= 0 {
		return dailyTokenBudget
	}
	return custom
}

// SetUserBudget fixe le budget journalier personnalisé d'un compte.
// tokens <= 0 supprime le budget personnalisé (retour au défaut global).
func (m *Manager) SetUserBudget(userID string, tokens int) error {
	if m.db == nil {
		return fmt.Errorf("suivi des crédits désactivé (pas de base)")
	}
	if userID == "" {
		return fmt.Errorf("user_id manquant")
	}
	if tokens <= 0 {
		_, err := m.db.Exec(`DELETE FROM appgen_budgets WHERE user_id = ?`, userID)
		return err
	}
	_, err := m.db.Exec(`
		INSERT INTO appgen_budgets (user_id, daily_tokens, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			daily_tokens = excluded.daily_tokens,
			updated_at = excluded.updated_at
	`, userID, tokens, time.Now().UnixMilli())
	return err
}

// UserBudget renvoie le budget personnalisé d'un compte (0 = pas de
// personnalisation, le défaut global s'applique).
func (m *Manager) UserBudget(userID string) int {
	if m.db == nil || userID == "" {
		return 0
	}
	var custom int
	if err := m.db.QueryRow(`SELECT daily_tokens FROM appgen_budgets WHERE user_id = ?`, userID).Scan(&custom); err != nil {
		return 0
	}
	return custom
}

// effectiveLimit renvoie le plafond par génération de ce compte (le budget
// personnalisé posé par l'admin remplace le plafond global GEN_TOKEN_LIMIT)
// et le plafond effectif de la prochaine génération, borné par les crédits
// restants aujourd'hui.
func (m *Manager) effectiveLimit(userID string, remaining int) (perGenCap, effLimit int) {
	perGenCap = m.limit
	if custom := m.UserBudget(userID); custom > 0 {
		perGenCap = custom
	}
	effLimit = perGenCap
	if remaining < effLimit {
		effLimit = remaining
	}
	return perGenCap, effLimit
}

// ResetCredits réinitialise les crédits du jour d'un compte (une ligne
// absente pour le jour courant = budget complet, comme la remise à zéro
// quotidienne). Renvoie le nombre de tokens qui étaient consommés.
func (m *Manager) ResetCredits(userID string) (int, error) {
	if m.db == nil {
		return 0, fmt.Errorf("suivi des crédits désactivé (pas de base)")
	}
	var used int
	_ = m.db.QueryRow(`SELECT tokens_used FROM appgen_credits WHERE user_id = ? AND day = ?`,
		userID, today()).Scan(&used)
	_, err := m.db.Exec(`DELETE FROM appgen_credits WHERE user_id = ? AND day = ?`, userID, today())
	return used, err
}

// ResetAllCredits réinitialise les crédits du jour de tous les comptes.
// Renvoie le nombre de comptes concernés.
func (m *Manager) ResetAllCredits() (int, error) {
	if m.db == nil {
		return 0, fmt.Errorf("suivi des crédits désactivé (pas de base)")
	}
	res, err := m.db.Exec(`DELETE FROM appgen_credits WHERE day = ?`, today())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// UsageToday renvoie la consommation du jour par utilisateur (user_id →
// tokens consommés). Les comptes sans ligne aujourd'hui n'y figurent pas.
func (m *Manager) UsageToday() map[string]int {
	usage := map[string]int{}
	if m.db == nil {
		return usage
	}
	rows, err := m.db.Query(`SELECT user_id, tokens_used FROM appgen_credits WHERE day = ?`, today())
	if err != nil {
		return usage
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var used int
		if rows.Scan(&id, &used) == nil {
			usage[id] = used
		}
	}
	return usage
}
