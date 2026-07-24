// Package ghclient encapsule les appels à l'API GitHub Actions nécessaires
// pour déclencher un workflow, suivre son exécution et récupérer l'APK produit.
package ghclient

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
	"time"
)

const apiBase = "https://api.github.com"

// Client parle à l'API GitHub pour un dépôt donné.
type Client struct {
	http     *http.Client
	token    string
	owner    string
	repo     string
	workflow string
}

// New construit un client pour owner/repo.
func New(token, owner, repo, workflow string) *Client {
	return &Client{
		http:     &http.Client{Timeout: 30 * time.Second},
		token:    token,
		owner:    owner,
		repo:     repo,
		workflow: workflow,
	}
}

func (c *Client) do(ctx context.Context, method, url string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.http.Do(req)
}

// Dispatch déclenche le workflow via workflow_dispatch sur la branche ref,
// en passant les inputs fournis.
func (c *Client) Dispatch(ctx context.Context, ref string, inputs map[string]string) error {
	payload, _ := json.Marshal(map[string]any{"ref": ref, "inputs": inputs})
	url := fmt.Sprintf("%s/repos/%s/%s/actions/workflows/%s/dispatches", apiBase, c.owner, c.repo, c.workflow)
	resp, err := c.do(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("dispatch a échoué (%d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// Run représente une exécution de workflow (partiel).
type Run struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`     // queued, in_progress, completed
	Conclusion string `json:"conclusion"` // success, failure, cancelled, ...
	HTMLURL    string `json:"html_url"`
}

// maxRunPages limite le nombre de pages parcourues (100 runs/page).
const maxRunPages = 5

// FindRunByName recherche le run dont le nom (run-name) correspond exactement à
// name, parmi les runs créés depuis `since`. Le filtre par date + la pagination
// rendent la corrélation fiable même quand beaucoup de builds tournent en
// parallèle. Renvoie nil si aucun ne correspond encore.
func (c *Client) FindRunByName(ctx context.Context, name string, since time.Time) (*Run, error) {
	// Marge pour absorber un éventuel décalage d'horloge entre serveur et GitHub.
	created := since.Add(-2 * time.Minute).UTC().Format(time.RFC3339)

	for page := 1; page <= maxRunPages; page++ {
		url := fmt.Sprintf(
			"%s/repos/%s/%s/actions/runs?event=workflow_dispatch&per_page=100&page=%d&created=%s",
			apiBase, c.owner, c.repo, page, neturl.QueryEscape(">="+created),
		)
		resp, err := c.do(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		var data struct {
			Total int   `json:"total_count"`
			Runs  []Run `json:"workflow_runs"`
		}
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("liste des runs a échoué (%d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
		}
		err = json.NewDecoder(resp.Body).Decode(&data)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		for i := range data.Runs {
			if data.Runs[i].Name == name {
				return &data.Runs[i], nil
			}
		}
		if len(data.Runs) < 100 {
			break // dernière page atteinte
		}
	}
	return nil, nil
}

// GetRun récupère l'état courant d'un run.
func (c *Client) GetRun(ctx context.Context, id int64) (*Run, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/actions/runs/%d", apiBase, c.owner, c.repo, id)
	resp, err := c.do(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get run a échoué (%d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var run Run
	if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
		return nil, err
	}
	return &run, nil
}

type artifact struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	ArchiveDownloadURL string `json:"archive_download_url"`
}

// DownloadAPK télécharge l'artifact du run et en extrait le premier fichier .apk.
func (c *Client) DownloadAPK(ctx context.Context, runID int64) ([]byte, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/actions/runs/%d/artifacts", apiBase, c.owner, c.repo, runID)
	resp, err := c.do(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("liste des artifacts a échoué (%d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var data struct {
		Artifacts []artifact `json:"artifacts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	if len(data.Artifacts) == 0 {
		return nil, fmt.Errorf("aucun artifact trouvé pour le run %d", runID)
	}

	// Télécharge le zip de l'artifact (le client HTTP suit la redirection signée).
	zipResp, err := c.do(ctx, http.MethodGet, data.Artifacts[0].ArchiveDownloadURL, nil)
	if err != nil {
		return nil, err
	}
	defer zipResp.Body.Close()
	if zipResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("téléchargement de l'artifact a échoué (%d)", zipResp.StatusCode)
	}
	raw, err := io.ReadAll(zipResp.Body)
	if err != nil {
		return nil, err
	}

	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, fmt.Errorf("artifact zip illisible: %w", err)
	}
	for _, f := range zr.File {
		if strings.HasSuffix(strings.ToLower(f.Name), ".apk") {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("aucun .apk dans l'artifact")
}
