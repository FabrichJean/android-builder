package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// loadDotEnv charge un fichier .env (KEY=VALUE par ligne) dans l'environnement,
// sans écraser les variables déjà définies. Silencieux si le fichier n'existe pas.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`) // retire d'éventuels guillemets
		if key != "" && os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
}

// Config regroupe la configuration du serveur, lue depuis l'environnement.
type Config struct {
	Port     string // port d'écoute HTTP
	Token    string // token GitHub (PAT ou fine-grained) avec droit Actions: read/write
	Owner    string // propriétaire du dépôt (user ou org)
	Repo     string // nom du dépôt contenant le workflow + le template Android
	Workflow string // nom de fichier du workflow, ex: build-apk.yml
	Ref      string // branche sur laquelle lancer le workflow, ex: main
	Releases string // dossier où sont écrits les APK produits
	Thumbs   string // dossier où sont écrites les miniatures (screenshots)
}

// Load lit la configuration depuis les variables d'environnement et valide
// les champs obligatoires.
func Load() (*Config, error) {
	// Charge .env si présent (les vraies variables d'env restent prioritaires).
	loadDotEnv(".env")

	c := &Config{
		Port:     env("PORT", "8080"),
		Token:    os.Getenv("GITHUB_TOKEN"),
		Owner:    os.Getenv("GITHUB_OWNER"),
		Repo:     os.Getenv("GITHUB_REPO"),
		Workflow: env("GITHUB_WORKFLOW", "build-apk.yml"),
		Ref:      env("GITHUB_REF", "main"),
		Releases: env("RELEASES_DIR", "releases"),
		Thumbs:   env("THUMBS_DIR", "thumbs"),
	}

	var missing []string
	if c.Token == "" {
		missing = append(missing, "GITHUB_TOKEN")
	}
	if c.Owner == "" {
		missing = append(missing, "GITHUB_OWNER")
	}
	if c.Repo == "" {
		missing = append(missing, "GITHUB_REPO")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("variables d'environnement manquantes: %v", missing)
	}
	return c, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
