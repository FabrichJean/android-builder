package ghclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
	"time"
)

// Health est le résultat d'un contrôle de disponibilité du build GitHub Actions.
type Health struct {
	OK     bool
	Reason string // explication lisible quand OK == false
}

const (
	minRateLimitRemaining = 100           // en dessous, le suivi des builds (polling) risque d'échouer
	startFailureWindow    = 6 * time.Hour // runs récents considérés pour détecter un blocage de facturation
	startFailureThreshold = 2             // nb de runs consécutifs « morts au démarrage » avant de déclarer indisponible
)

func (c *Client) api() string {
	if c.baseURL != "" {
		return c.baseURL
	}
	return apiBase
}

// getJSON appelle l'API et décode la réponse JSON dans out (si non nil).
func (c *Client) getJSON(ctx context.Context, path string, out any) (int, http.Header, error) {
	resp, err := c.do(ctx, http.MethodGet, c.api()+path, nil)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, resp.Header, err
		}
	}
	return resp.StatusCode, resp.Header, nil
}

// CheckHealth vérifie que lancer un build via GitHub Actions a des chances de
// réussir : token valide, dépôt/branche/workflow accessibles et actifs, quota
// de l'API, minutes Actions non épuisées (dépôts privés) et absence de runs
// récents bloqués au démarrage (typiquement : facturation / limite de dépense).
// Les vérifications « best effort » (facturation) ne bloquent jamais quand
// l'API ne répond pas clairement : mieux vaut laisser essayer que bloquer à tort.
func (c *Client) CheckHealth(ctx context.Context, ref string) Health {
	fail := func(f string, a ...any) Health { return Health{Reason: fmt.Sprintf(f, a...)} }
	repoPath := fmt.Sprintf("/repos/%s/%s", c.owner, c.repo)

	// 1. Quota de l'API (cet appel ne consomme pas le quota).
	var rl struct {
		Resources struct {
			Core struct {
				Remaining int   `json:"remaining"`
				Reset     int64 `json:"reset"`
			} `json:"core"`
		} `json:"resources"`
	}
	if code, _, err := c.getJSON(ctx, "/rate_limit", &rl); err != nil {
		return fail("API GitHub injoignable : %v", err)
	} else if code == http.StatusUnauthorized {
		return fail("token GitHub invalide ou expiré (GITHUB_TOKEN)")
	} else if code == http.StatusOK && rl.Resources.Core.Remaining < minRateLimitRemaining {
		return fail("quota de l'API GitHub presque épuisé (%d requêtes restantes, reprise à %s)",
			rl.Resources.Core.Remaining, time.Unix(rl.Resources.Core.Reset, 0).Format("15:04"))
	}

	// 2. Dépôt.
	var repo struct {
		Private  bool `json:"private"`
		Archived bool `json:"archived"`
		Owner    struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"owner"`
	}
	switch code, _, err := c.getJSON(ctx, repoPath, &repo); {
	case err != nil:
		return fail("API GitHub injoignable : %v", err)
	case code == http.StatusUnauthorized:
		return fail("token GitHub invalide ou expiré (GITHUB_TOKEN)")
	case code == http.StatusForbidden:
		return fail("accès refusé au dépôt %s/%s (permissions du token)", c.owner, c.repo)
	case code == http.StatusNotFound:
		return fail("dépôt %s/%s introuvable, ou token sans accès", c.owner, c.repo)
	case code != http.StatusOK:
		return fail("API GitHub indisponible (HTTP %d)", code)
	}
	if repo.Archived {
		return fail("le dépôt %s/%s est archivé : impossible de lancer un workflow", c.owner, c.repo)
	}

	// 3. Branche de build.
	if ref != "" {
		if code, _, err := c.getJSON(ctx, repoPath+"/branches/"+neturl.PathEscape(ref), nil); err == nil && code == http.StatusNotFound {
			return fail("branche de build %q introuvable (GITHUB_REF)", ref)
		}
	}

	// 4. Workflow présent et actif.
	var wf struct {
		State string `json:"state"`
	}
	switch code, _, err := c.getJSON(ctx, repoPath+"/actions/workflows/"+neturl.PathEscape(c.workflow), &wf); {
	case err != nil:
		return fail("API GitHub injoignable : %v", err)
	case code == http.StatusNotFound:
		return fail("workflow %q introuvable dans le dépôt (GITHUB_WORKFLOW)", c.workflow)
	case code == http.StatusOK && wf.State != "active":
		return fail("workflow %q désactivé (%s) : réactive-le dans l'onglet Actions", c.workflow, wf.State)
	}

	// 5. Minutes Actions (dépôts privés uniquement : gratuit en public).
	if repo.Private {
		billing := "/users/" + repo.Owner.Login + "/settings/billing/actions"
		if repo.Owner.Type == "Organization" {
			billing = "/orgs/" + repo.Owner.Login + "/settings/billing/actions"
		}
		var b struct {
			Used     float64 `json:"total_minutes_used"`
			Paid     float64 `json:"total_paid_minutes_used"`
			Included float64 `json:"included_minutes"`
		}
		// Un code ≠ 200 (scope manquant, endpoint retiré…) = on ne sait pas : on laisse passer.
		if code, _, err := c.getJSON(ctx, billing, &b); err == nil && code == http.StatusOK &&
			b.Included > 0 && b.Used >= b.Included && b.Paid == 0 {
			return fail("minutes GitHub Actions incluses épuisées (%.0f/%.0f) et aucun budget payant", b.Used, b.Included)
		}
	}

	// 6. Runs récents morts avant démarrage (facturation, limite de dépense…).
	if h := c.recentStartFailures(ctx, repoPath); !h.OK {
		return h
	}
	return Health{OK: true}
}

// recentStartFailures détecte les derniers runs terminés en échec SANS avoir
// exécuté la moindre étape : c'est la signature d'un compte bloqué (paiement
// échoué, limite de dépense atteinte). Un seul run ne suffit pas ; ils doivent
// être consécutifs et récents, ce qui laisse le statut se rétablir de lui-même.
func (c *Client) recentStartFailures(ctx context.Context, repoPath string) Health {
	var runs struct {
		Runs []struct {
			ID         int64     `json:"id"`
			Conclusion string    `json:"conclusion"`
			CreatedAt  time.Time `json:"created_at"`
		} `json:"workflow_runs"`
	}
	path := fmt.Sprintf("%s/actions/workflows/%s/runs?status=completed&per_page=%d", repoPath, neturl.PathEscape(c.workflow), startFailureThreshold)
	if code, _, err := c.getJSON(ctx, path, &runs); err != nil || code != http.StatusOK || len(runs.Runs) < startFailureThreshold {
		return Health{OK: true}
	}
	for _, r := range runs.Runs {
		if time.Since(r.CreatedAt) > startFailureWindow {
			return Health{OK: true}
		}
		started := r.Conclusion == "startup_failure"
		if !started && r.Conclusion == "failure" {
			var jobs struct {
				Jobs []struct {
					Steps []json.RawMessage `json:"steps"`
				} `json:"jobs"`
			}
			code, _, err := c.getJSON(ctx, fmt.Sprintf("%s/actions/runs/%d/jobs", repoPath, r.ID), &jobs)
			if err != nil || code != http.StatusOK || len(jobs.Jobs) == 0 {
				return Health{OK: true}
			}
			started = true
			for _, j := range jobs.Jobs {
				if len(j.Steps) > 0 {
					started = false
				}
			}
		}
		if !started {
			return Health{OK: true}
		}
	}
	return Health{Reason: fmt.Sprintf("les %d derniers builds GitHub ont échoué avant de démarrer (facturation, limite de dépense ou quota Actions ?)", startFailureThreshold)}
}
