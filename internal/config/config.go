package config

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
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
	DB       string // chemin du fichier SQLite (builds + utilisateurs)

	// Connexion Google (optionnelle) : historique de builds privé par compte.
	// Si GoogleClientID/Secret sont vides, la connexion est simplement désactivée
	// et l'app fonctionne comme avant (historique local au navigateur, partagé).
	// Noms de variables préfixés LOGIN_ pour ne pas entrer en collision avec un
	// éventuel autre usage de GOOGLE_CLIENT_ID/SECRET dans le même .env
	// (ex. une autre app lisant le même fichier pour l'envoi Gmail).
	BaseURL            string // URL publique de l'app, ex: http://localhost:8080 (sert à construire le redirect Google)
	GoogleClientID     string
	GoogleClientSecret string
	SessionSecret      string // signe les cookies de session ; auto-généré si absent (sessions perdues au redémarrage)

	// Génération d'app par IA (CLI claude du serveur, réservée aux comptes).
	ClaudeBin     string // binaire du CLI claude (défaut: "claude" dans le PATH)
	GenTokenLimit int    // plafond de tokens de sortie pour une génération
}

// Load lit la configuration depuis les variables d'environnement et valide
// les champs obligatoires.
func Load() (*Config, error) {
	// Charge .env si présent (les vraies variables d'env restent prioritaires).
	loadDotEnv(".env")

	c := &Config{
		Port:               env("PORT", "8080"),
		Token:              os.Getenv("GITHUB_TOKEN"),
		Owner:              os.Getenv("GITHUB_OWNER"),
		Repo:               os.Getenv("GITHUB_REPO"),
		Workflow:           env("GITHUB_WORKFLOW", "build-apk.yml"),
		Ref:                env("GITHUB_REF", "main"),
		Releases:           env("RELEASES_DIR", "releases"),
		Thumbs:             env("THUMBS_DIR", "thumbs"),
		DB:                 env("DB_PATH", "data.db"),
		BaseURL:            strings.TrimRight(env("BASE_URL", "http://localhost:"+env("PORT", "8080")), "/"),
		GoogleClientID:     os.Getenv("GOOGLE_LOGIN_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("GOOGLE_LOGIN_CLIENT_SECRET"),
		SessionSecret:      os.Getenv("SESSION_SECRET"),
		ClaudeBin:          env("CLAUDE_BIN", "claude"),
		GenTokenLimit:      envInt("GEN_TOKEN_LIMIT", 10000),
	}
	if c.SessionSecret == "" {
		// Pas fourni : on en génère un pour cette exécution (les sessions ne
		// survivront pas à un redémarrage, mais l'appli reste utilisable).
		b := make([]byte, 32)
		if _, err := rand.Read(b); err == nil {
			c.SessionSecret = hex.EncodeToString(b)
		}
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

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
