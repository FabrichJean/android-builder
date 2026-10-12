//go:build !windows

package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/example/android-builder/internal/config"
)

// fakeEnv construit un SDK Android, un JDK (script `java`) et un template
// minimaux dans un dossier temporaire, et renvoie un Server configuré dessus.
func fakeEnv(t *testing.T, javaVersion string) *Server {
	t.Helper()
	old := minFreeDisk
	minFreeDisk = 0 // le test ne doit pas dépendre de l'espace disque réel de la machine
	t.Cleanup(func() { minFreeDisk = old })
	root := t.TempDir()
	mk := func(p string) { os.MkdirAll(filepath.Join(root, p), 0o755) }
	mk("sdk/platforms/" + requiredPlatform)
	mk("sdk/build-tools")
	mk("jdk/bin")
	mk("tpl/app")
	os.WriteFile(filepath.Join(root, "tpl/app/build.gradle.kts"), nil, 0o644)
	os.WriteFile(filepath.Join(root, "tpl/gradlew"), []byte("#!/bin/sh\n"), 0o755)
	os.WriteFile(filepath.Join(root, "jdk/bin/java"), []byte("#!/bin/sh\necho 'openjdk version \""+javaVersion+"\" 2026-01-01' >&2\n"), 0o755)
	t.Setenv("JAVA_HOME", filepath.Join(root, "jdk"))

	return &Server{
		cfg: &config.Config{
			LocalBuild: true, AndroidHome: filepath.Join(root, "sdk"),
			TemplateDir: filepath.Join(root, "tpl"), Releases: root,
		},
		limiter: newLocalLimiter(nil, 5, 15, 2),
		health:  newHealthMonitor(),
	}
}

func TestCheckServerBuild(t *testing.T) {
	cases := []struct {
		name   string
		java   string
		mutate func(s *Server)
		want   string // "" = disponible
	}{
		{"ok", "17.0.20", func(s *Server) {}, ""},
		{"LOCAL_BUILD=false", "17.0.1", func(s *Server) { s.cfg.LocalBuild = false }, "LOCAL_BUILD=false"},
		{"pas de SDK", "17.0.1", func(s *Server) { s.cfg.AndroidHome = "" }, "SDK Android introuvable"},
		{"plateforme manquante", "17.0.1", func(s *Server) { os.Remove(filepath.Join(s.cfg.AndroidHome, "platforms", requiredPlatform)) }, "plateforme"},
		{"build-tools manquants", "17.0.1", func(s *Server) { os.Remove(filepath.Join(s.cfg.AndroidHome, "build-tools")) }, "build-tools"},
		{"JDK trop ancien", "11.0.2", func(s *Server) {}, "JDK 11"},
		{"JDK trop récent", "25.0.1", func(s *Server) {}, "JDK 25"},
		{"JDK absent", "17.0.1", func(s *Server) { t.Setenv("JAVA_HOME", filepath.Join(t.TempDir(), "nope")) }, "JDK introuvable"},
		{"template absent", "17.0.1", func(s *Server) { s.cfg.TemplateDir = t.TempDir() }, "template Android introuvable"},
		{"gradlew non exécutable", "17.0.1", func(s *Server) { os.Chmod(filepath.Join(s.cfg.TemplateDir, "gradlew"), 0o644) }, "gradlew"},
		{"GRADLE_BIN introuvable", "17.0.1", func(s *Server) { s.cfg.GradleBin = "gradle-qui-nexiste-pas" }, "GRADLE_BIN"},
		{"file saturée", "17.0.1", func(s *Server) {
			s.limiter.Acquire("a", false)
			s.limiter.Acquire("b", false)
		}, "file d'attente"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := fakeEnv(t, tc.java)
			tc.mutate(s)
			ok, reason := s.checkServerBuild()
			if tc.want == "" {
				if !ok {
					t.Fatalf("attendu disponible, obtenu indisponible : %s", reason)
				}
				return
			}
			if ok || !strings.Contains(reason, tc.want) {
				t.Fatalf("ok=%v raison=%q, attendu une raison contenant %q", ok, reason, tc.want)
			}
		})
	}
}

func TestCheckServerBuildLowDisk(t *testing.T) {
	s := fakeEnv(t, "17.0.1")
	minFreeDisk = ^uint64(0) // exige « tout l'espace » : forcément insuffisant
	ok, reason := s.checkServerBuild()
	if ok || !strings.Contains(reason, "espace disque insuffisant") {
		t.Fatalf("ok=%v raison=%q", ok, reason)
	}
}

func TestHealthMonitorDefaultsToAvailable(t *testing.T) {
	h := newHealthMonitor()
	if !h.get(methodGitHub).Available || !h.get(methodServer).Available {
		t.Fatal("sans contrôle abouti, les méthodes doivent rester disponibles")
	}
	h.set(methodServer, false, "panne")
	if m := h.get(methodServer); m.Available || m.Reason != "panne" || m.CheckedAt.IsZero() {
		t.Fatalf("état inattendu: %+v", m)
	}
	if !h.get(methodGitHub).Available {
		t.Fatal("les méthodes sont indépendantes")
	}
}
