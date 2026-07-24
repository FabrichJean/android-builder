package config

import (
	"fmt"
	"os"
)

// Config regroupe la configuration du serveur, lue depuis l'environnement.
type Config struct {
	Port     string // port d'écoute HTTP
	Token    string // token GitHub (PAT ou fine-grained) avec droit Actions: read/write
	Owner    string // propriétaire du dépôt (user ou org)
	Repo     string // nom du dépôt contenant le workflow + le template Android
	Workflow string // nom de fichier du workflow, ex: build-apk.yml
	Ref      string // branche sur laquelle lancer le workflow, ex: main
	Releases string // dossier où sont écrits les APK produits
}

// Load lit la configuration depuis les variables d'environnement et valide
// les champs obligatoires.
func Load() (*Config, error) {
	c := &Config{
		Port:     env("PORT", "8080"),
		Token:    os.Getenv("GITHUB_TOKEN"),
		Owner:    os.Getenv("GITHUB_OWNER"),
		Repo:     os.Getenv("GITHUB_REPO"),
		Workflow: env("GITHUB_WORKFLOW", "build-apk.yml"),
		Ref:      env("GITHUB_REF", "main"),
		Releases: env("RELEASES_DIR", "releases"),
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
