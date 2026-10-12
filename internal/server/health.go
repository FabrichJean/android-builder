package server

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// Disponibilité des méthodes de build, contrôlée en arrière-plan : le
// formulaire affiche un badge « Non disponible » (avec la raison) et le
// serveur refuse les builds sur une méthode en panne, plutôt que de laisser
// l'utilisateur lancer un build voué à l'échec.
const (
	serverHealthEvery = time.Minute     // contrôles locaux : bon marché
	githubHealthEvery = 5 * time.Minute // appels API GitHub : plus espacés
	requiredPlatform  = "android-34"    // compileSdk du template
)

// methodHealth est l'état d'une méthode de build.
type methodHealth struct {
	Available bool      `json:"available"`
	Reason    string    `json:"reason,omitempty"`
	CheckedAt time.Time `json:"checked_at,omitempty"` // zéro = pas encore contrôlée
}

type healthMonitor struct {
	mu     sync.RWMutex
	github methodHealth
	server methodHealth
}

// Tant qu'aucun contrôle n'a abouti, on considère la méthode disponible : on ne
// bloque pas les builds sur une simple absence d'information.
func newHealthMonitor() *healthMonitor {
	return &healthMonitor{github: methodHealth{Available: true}, server: methodHealth{Available: true}}
}

func (h *healthMonitor) get(method string) methodHealth {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if method == methodServer {
		return h.server
	}
	return h.github
}

func (h *healthMonitor) set(method string, ok bool, reason string) {
	m := methodHealth{Available: ok, Reason: reason, CheckedAt: time.Now()}
	h.mu.Lock()
	defer h.mu.Unlock()
	if method == methodServer {
		h.server = m
	} else {
		h.github = m
	}
}

// StartHealthChecks lance les contrôles périodiques (un premier tout de suite).
// Ils s'arrêtent avec ctx.
func (s *Server) StartHealthChecks(ctx context.Context) {
	run := func(method string, every time.Duration, check func(context.Context) (bool, string)) {
		go func() {
			t := time.NewTicker(every)
			defer t.Stop()
			for {
				cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
				prev := s.health.get(method)
				ok, reason := check(cctx)
				cancel()
				s.health.set(method, ok, reason)
				if !prev.CheckedAt.IsZero() && prev.Available != ok || prev.CheckedAt.IsZero() && !ok {
					s.log.Warn("disponibilité de la méthode de build modifiée", "method", method, "available", ok, "reason", reason)
				}
				select {
				case <-ctx.Done():
					return
				case <-t.C:
				}
			}
		}()
	}
	run(methodGitHub, githubHealthEvery, func(c context.Context) (bool, string) {
		h := s.gh.CheckHealth(c, s.cfg.Ref)
		return h.OK, h.Reason
	})
	run(methodServer, serverHealthEvery, func(context.Context) (bool, string) {
		return s.checkServerBuild()
	})
}

// methodUnavailable renvoie la raison si la méthode est actuellement en panne.
func (s *Server) methodUnavailable(method string) (string, bool) {
	h := s.health.get(method)
	return h.Reason, !h.Available
}

// minFreeDisk : Gradle + copie du template + APK (variable pour les tests).
var minFreeDisk uint64 = 2 << 30

var javaMajorRe = regexp.MustCompile(`version "(\d+)`)

// checkServerBuild vérifie que ce serveur peut compiler un APK : option activée,
// SDK Android complet, JDK 17-21, Gradle (wrapper) et template présents,
// espace disque suffisant, file d'attente non saturée.
func (s *Server) checkServerBuild() (bool, string) {
	cfg := s.cfg
	if !cfg.LocalBuild {
		return false, "désactivé sur ce serveur (LOCAL_BUILD=false)"
	}
	if cfg.AndroidHome == "" {
		return false, "SDK Android introuvable sur le serveur (ANDROID_HOME)"
	}
	if !isDir(filepath.Join(cfg.AndroidHome, "platforms", requiredPlatform)) {
		return false, fmt.Sprintf("SDK Android incomplet : plateforme %s manquante", requiredPlatform)
	}
	if !isDir(filepath.Join(cfg.AndroidHome, "build-tools")) {
		return false, "SDK Android incomplet : build-tools manquants"
	}

	java := "java"
	if jh := os.Getenv("JAVA_HOME"); jh != "" {
		java = filepath.Join(jh, "bin", "java")
	}
	if major, err := javaMajor(java); err != nil {
		return false, "JDK introuvable sur le serveur (JAVA_HOME / java)"
	} else if major < 17 || major > 21 {
		return false, fmt.Sprintf("JDK %d détecté : la compilation exige un JDK 17 à 21", major)
	}

	if !isFile(filepath.Join(cfg.TemplateDir, "app", "build.gradle.kts")) {
		return false, fmt.Sprintf("template Android introuvable (%s)", cfg.TemplateDir)
	}
	if cfg.GradleBin != "" {
		if _, err := exec.LookPath(cfg.GradleBin); err != nil {
			return false, fmt.Sprintf("commande Gradle introuvable (GRADLE_BIN=%s)", cfg.GradleBin)
		}
	} else if st, err := os.Stat(filepath.Join(cfg.TemplateDir, "gradlew")); err != nil || st.Mode()&0o111 == 0 {
		return false, "gradlew absent ou non exécutable dans le template"
	}

	for _, dir := range []string{os.TempDir(), cfg.Releases} {
		if free, err := freeDiskBytes(dir); err == nil && free < minFreeDisk {
			return false, fmt.Sprintf("espace disque insuffisant sur le serveur (%d Mo libres, %d Mo requis)", free>>20, minFreeDisk>>20)
		}
	}

	if s.limiter.Full() {
		return false, "file d'attente des builds saturée, réessaie dans quelques minutes"
	}
	return true, ""
}

// javaMajor lance `java -version` et renvoie la version majeure.
func javaMajor(bin string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-version").CombinedOutput() // la version sort sur stderr
	if err != nil {
		return 0, err
	}
	m := javaMajorRe.FindSubmatch(out)
	if m == nil {
		return 0, fmt.Errorf("version Java illisible")
	}
	return strconv.Atoi(string(m[1]))
}

func isDir(p string) bool  { st, err := os.Stat(p); return err == nil && st.IsDir() }
func isFile(p string) bool { st, err := os.Stat(p); return err == nil && !st.IsDir() }
