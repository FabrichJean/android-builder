package appgen

// Test manuel de bout en bout : invoque RÉELLEMENT le CLI claude (consomme des
// tokens). Ne tourne jamais par défaut — à lancer explicitement :
//
//	APPGEN_MANUAL=1 go test -v -timeout 10m -run TestManualEndToEnd ./internal/appgen

import (
	"archive/zip"
	"bytes"
	"context"
	"log/slog"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestManualEndToEnd(t *testing.T) {
	if os.Getenv("APPGEN_MANUAL") != "1" {
		t.Skip("test manuel (vraie invocation claude CLI) — définir APPGEN_MANUAL=1 pour l'exécuter")
	}
	bin, err := exec.LookPath("claude")
	if err != nil {
		t.Fatal("CLI claude introuvable")
	}
	m := New(bin, 5000, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	s, err := m.Prepare(context.Background(), "user-test", "une page qui affiche bonjour et un compteur de clics")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	t.Logf("status=%s app_name=%q estimated=%d limit=%d", s.Status, s.AppName, s.EstimatedTokens, s.TokenLimit)
	for _, q := range s.Questions {
		t.Logf("  Q[%s] %s -> %v", q.ID, q.Label, q.Options)
	}
	if s.Status != StatusAsking {
		t.Fatalf("statut inattendu après Prepare: %s (%s)", s.Status, s.Error)
	}
	if len(s.Questions) == 0 {
		t.Fatal("aucune question de clarification renvoyée")
	}

	answers := map[string]string{}
	for _, q := range s.Questions {
		if len(q.Options) > 0 {
			answers[q.ID] = q.Options[0]
		}
	}
	if err := m.StartGenerate(s.ID, "user-test", answers); err != nil {
		t.Fatalf("StartGenerate: %v", err)
	}
	deadline := time.After(8 * time.Minute)
	for {
		select {
		case <-deadline:
			t.Fatal("délai dépassé")
		case <-time.After(2 * time.Second):
		}
		cur, ok := m.Get(s.ID, "user-test")
		if !ok {
			t.Fatal("session disparue")
		}
		t.Logf("  status=%s tokens=%d", cur.Status, cur.TokensUsed)
		if cur.Status == StatusSuccess {
			data, ok := m.Zip(s.ID, "user-test")
			if !ok {
				t.Fatal("zip indisponible malgré le succès")
			}
			zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Fatalf("zip illisible: %v", err)
			}
			if len(zr.File) != 1 || zr.File[0].Name != "index.html" {
				t.Fatalf("contenu de zip inattendu: %v", zr.File)
			}
			t.Logf("OK — index.html de %d octets, %d tokens consommés", zr.File[0].UncompressedSize64, cur.TokensUsed)
			return
		}
		if cur.Status == StatusFailed || cur.Status == StatusOverBudget {
			t.Fatalf("génération terminée en %s: %s", cur.Status, cur.Error)
		}
	}
}
