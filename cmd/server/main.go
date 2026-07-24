// Commande server : API HTTP qui déclenche un build d'APK WebView via GitHub Actions.
package main

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/example/android-builder/internal/buildstore"
	"github.com/example/android-builder/internal/config"
	"github.com/example/android-builder/internal/ghclient"
	"github.com/example/android-builder/internal/server"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		log.Error("configuration invalide", "err", err)
		os.Exit(1)
	}

	if err := os.MkdirAll(cfg.Releases, 0o755); err != nil {
		log.Error("dossier releases inaccessible", "dir", cfg.Releases, "err", err)
		os.Exit(1)
	}

	gh := ghclient.New(cfg.Token, cfg.Owner, cfg.Repo, cfg.Workflow)
	store := buildstore.New()
	srv := server.New(cfg, gh, store, log)

	httpSrv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Info("serveur démarré", "port", cfg.Port, "repo", cfg.Owner+"/"+cfg.Repo)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("serveur arrêté", "err", err)
		os.Exit(1)
	}
}
