package appgen

import (
	"log/slog"
	"os"
	"testing"
)

// TestUserBudgetsAndReset vérifie les budgets personnalisés par utilisateur
// et la remise à zéro des crédits (un compte / tous), sans appel CLI.
func TestUserBudgetsAndReset(t *testing.T) {
	m := New("claude", 10000, openTestDB(t), slog.New(slog.NewTextHandler(os.Stderr, nil)))

	// Sans personnalisation : budget par défaut, rien de consommé.
	if got := m.DailyTokenBudget("u1"); got != dailyTokenBudget {
		t.Fatalf("budget par défaut = %d, attendu %d", got, dailyTokenBudget)
	}
	if got := m.RemainingTokens("u1"); got != dailyTokenBudget {
		t.Fatalf("restant initial = %d, attendu %d", got, dailyTokenBudget)
	}

	// Budget personnalisé : remplace le défaut, et le restant en découle.
	if err := m.SetUserBudget("u1", 3000); err != nil {
		t.Fatalf("SetUserBudget: %v", err)
	}
	if got := m.DailyTokenBudget("u1"); got != 3000 {
		t.Fatalf("budget personnalisé = %d, attendu 3000", got)
	}
	m.addUsage("u1", 2500)
	if got := m.RemainingTokens("u1"); got != 500 {
		t.Fatalf("restant après conso = %d, attendu 500", got)
	}

	// Reset d'un compte : le restant revient au budget complet.
	used, err := m.ResetCredits("u1")
	if err != nil || used != 2500 {
		t.Fatalf("ResetCredits = (%d, %v), attendu (2500, nil)", used, err)
	}
	if got := m.RemainingTokens("u1"); got != 3000 {
		t.Fatalf("restant après reset = %d, attendu 3000", got)
	}

	// Budget <= 0 : retour au défaut global.
	if err := m.SetUserBudget("u1", 0); err != nil {
		t.Fatalf("SetUserBudget(0): %v", err)
	}
	if got := m.DailyTokenBudget("u1"); got != dailyTokenBudget {
		t.Fatalf("budget après suppression = %d, attendu %d", got, dailyTokenBudget)
	}

	// Le budget admin remplace aussi le plafond PAR GÉNÉRATION : un budget
	// supérieur à GEN_TOKEN_LIMIT (ici 10000) doit s'appliquer tel quel, et
	// le plafond effectif reste borné par les crédits restants du jour.
	if err := m.SetUserBudget("u3", 25000); err != nil {
		t.Fatalf("SetUserBudget(25000): %v", err)
	}
	if cap, eff := m.effectiveLimit("u3", m.RemainingTokens("u3")); cap != 25000 || eff != 25000 {
		t.Fatalf("effectiveLimit(u3) = (%d, %d), attendu (25000, 25000)", cap, eff)
	}
	m.addUsage("u3", 24000)
	if _, eff := m.effectiveLimit("u3", m.RemainingTokens("u3")); eff != 1000 {
		t.Fatalf("effectiveLimit(u3) après conso = %d, attendu 1000", eff)
	}
	// Sans budget personnalisé : le plafond global reste la référence.
	if cap, _ := m.effectiveLimit("u4", m.RemainingTokens("u4")); cap != 10000 {
		t.Fatalf("effectiveLimit(u4) = %d, attendu 10000 (GEN_TOKEN_LIMIT)", cap)
	}

	// Reset global : toutes les consos du jour disparaissent.
	m.addUsage("u1", 100)
	m.addUsage("u2", 200)
	if usage := m.UsageToday(); len(usage) != 3 { // u1, u2 + la conso de u3 ci-dessus
		t.Fatalf("UsageToday = %v, attendu 3 comptes", usage)
	}
	n, err := m.ResetAllCredits()
	if err != nil || n != 3 {
		t.Fatalf("ResetAllCredits = (%d, %v), attendu (3, nil)", n, err)
	}
	if usage := m.UsageToday(); len(usage) != 0 {
		t.Fatalf("UsageToday après reset global = %v, attendu vide", usage)
	}
}
