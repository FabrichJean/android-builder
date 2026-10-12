package ghclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
	"regexp"
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
	billingFailureWindow  = 3 * time.Hour // un seul run « facturation » récent suffit à bloquer
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

// billingRe reconnaît les annotations GitHub d'un job refusé pour cause de
// facturation / limite de dépense : un seul run suffit alors à conclure.
var billingRe = regexp.MustCompile(`(?i)billing|spending limit|payments have failed|account is locked`)

// recentStartFailures détecte les derniers runs terminés en échec SANS avoir
// exécuté la moindre étape : c'est la signature d'un compte bloqué (paiement
// échoué, compte verrouillé, limite de dépense atteinte).
//   - un run dont l'annotation parle de facturation suffit, s'il date de moins
//     de billingFailureWindow : sans nouveau build, rien d'autre ne peut le
//     « guérir », d'où cette fenêtre courte (le prochain essai refait le test) ;
//   - sinon, il faut startFailureThreshold runs consécutifs, dans startFailureWindow.
func (c *Client) recentStartFailures(ctx context.Context, repoPath string) Health {
	var runs struct {
		Runs []struct {
			ID         int64     `json:"id"`
			Conclusion string    `json:"conclusion"`
			CreatedAt  time.Time `json:"created_at"`
		} `json:"workflow_runs"`
	}
	path := fmt.Sprintf("%s/actions/workflows/%s/runs?status=completed&per_page=%d", repoPath, neturl.PathEscape(c.workflow), startFailureThreshold)
	if code, _, err := c.getJSON(ctx, path, &runs); err != nil || code != http.StatusOK || len(runs.Runs) == 0 {
		return Health{OK: true}
	}
	consecutive := 0
	for i, r := range runs.Runs {
		age := time.Since(r.CreatedAt)
		if age > startFailureWindow {
			break
		}
		dead, reason := c.startFailure(ctx, repoPath, r.ID, r.Conclusion)
		if !dead {
			break
		}
		if i == 0 && age <= billingFailureWindow && billingRe.MatchString(reason) {
			return Health{Reason: "GitHub refuse de démarrer les builds : " + reason}
		}
		consecutive++
	}
	if consecutive >= startFailureThreshold {
		return Health{Reason: fmt.Sprintf("les %d derniers builds GitHub ont échoué avant de démarrer (facturation, limite de dépense ou quota Actions ?)", startFailureThreshold)}
	}
	return Health{OK: true}
}

// startFailure indique si le run est mort avant d'exécuter une seule étape, et
// renvoie le message d'annotation de GitHub expliquant pourquoi (s'il existe).
func (c *Client) startFailure(ctx context.Context, repoPath string, runID int64, conclusion string) (bool, string) {
	if conclusion != "startup_failure" && conclusion != "failure" {
		return false, ""
	}
	var jobs struct {
		Jobs []struct {
			ID    int64             `json:"id"`
			Steps []json.RawMessage `json:"steps"`
		} `json:"jobs"`
	}
	if code, _, err := c.getJSON(ctx, fmt.Sprintf("%s/actions/runs/%d/jobs", repoPath, runID), &jobs); err != nil || code != http.StatusOK {
		return false, ""
	}
	if len(jobs.Jobs) == 0 {
		return conclusion == "startup_failure", ""
	}
	reason := ""
	for _, j := range jobs.Jobs {
		if len(j.Steps) > 0 {
			return false, ""
		}
		if reason == "" {
			reason = c.failureAnnotation(ctx, repoPath, j.ID)
		}
	}
	return true, reason
}

// failureAnnotation renvoie le message d'erreur (annotation « failure ») d'un job.
func (c *Client) failureAnnotation(ctx context.Context, repoPath string, jobID int64) string {
	var anns []struct {
		Level   string `json:"annotation_level"`
		Message string `json:"message"`
	}
	if code, _, err := c.getJSON(ctx, fmt.Sprintf("%s/check-runs/%d/annotations", repoPath, jobID), &anns); err != nil || code != http.StatusOK {
		return ""
	}
	for _, a := range anns {
		if a.Level == "failure" {
			return a.Message
		}
	}
	return ""
}

// FailureReason explique l'échec d'un run quand GitHub n'a exécuté aucune
// étape (compte verrouillé, facturation…). Chaîne vide si le run a démarré
// normalement : l'échec est alors dans les journaux du job.
func (c *Client) FailureReason(ctx context.Context, runID int64, conclusion string) string {
	repoPath := fmt.Sprintf("/repos/%s/%s", c.owner, c.repo)
	if dead, reason := c.startFailure(ctx, repoPath, runID, conclusion); dead {
		if reason == "" {
			reason = "le job n'a pas démarré"
		}
		return reason
	}
	return ""
}

// IsBillingFailure indique si une raison d'échec relève de la facturation.
func IsBillingFailure(reason string) bool { return billingRe.MatchString(reason) }
