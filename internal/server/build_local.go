package server

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/example/android-builder/internal/buildstore"
)

// Méthodes de build proposées dans le formulaire.
const (
	methodGitHub = "github" // workflow GitHub Actions (défaut)
	methodServer = "server" // compilation locale par ce serveur
)

// localBuildAvailable indique si le serveur peut compiler lui-même un APK.
func (s *Server) localBuildAvailable() bool {
	return s.cfg.LocalBuild && s.cfg.AndroidHome != ""
}

// parseMethod normalise le champ build_method (vide = GitHub Actions).
// Le build par le serveur consomme du CPU/RAM locaux : il est réservé aux
// comptes connectés (les builds GitHub Actions restent ouverts à tous).
func (s *Server) parseMethod(r *http.Request, v string) (string, error) {
	switch strings.TrimSpace(v) {
	case "", methodGitHub:
		return methodGitHub, nil
	case methodServer:
		if !s.localBuildAvailable() {
			return "", fmt.Errorf("le build par le serveur n'est pas disponible (SDK Android introuvable ou LOCAL_BUILD=false)")
		}
		if s.currentUserID(r) == "" {
			return "", errLoginRequired
		}
		return methodServer, nil
	}
	return "", fmt.Errorf("build_method invalide (attendu: %q ou %q)", methodGitHub, methodServer)
}

var errLoginRequired = errors.New("connecte-toi avec Google pour compiler par le serveur")

// handleBuildMethods indique à l'interface quelles méthodes de build existent
// et si l'utilisateur courant a le droit d'utiliser la compilation locale.
func (s *Server) handleBuildMethods(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"github":       true,
		"server":       s.localBuildAvailable(),
		"server_login": s.localBuildAvailable() && s.currentUserID(r) == "",
	})
}

// localParams regroupe tout ce dont le build local a besoin (équivalent des
// inputs du workflow GitHub + des assets de la release temporaire).
type localParams struct {
	ID, AppURL, AppName, Package, VersionName, VersionCode, SplashBg           string
	HideScrollbar, DebugConsole, FixImages, BackgroundPlayer, PictureInPicture bool
	Dist, Icon                                                                 []byte // optionnels
}

// Étapes affichées dans l'interface (même format que les étapes GitHub).
var localSteps = []string{"Préparer le projet", "Compiler l'APK (Gradle)", "Récupérer l'APK"}

// runLocal compile l'APK sur ce serveur. Une seule compilation à la fois :
// les suivantes restent "pending" en attendant leur tour.
func (s *Server) runLocal(p localParams) {
	log := s.log.With("build_id", p.ID, "method", methodServer)
	s.localSem <- struct{}{}
	defer func() { <-s.localSem }()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	steps := make([]buildstore.Step, len(localSteps))
	for i, n := range localSteps {
		steps[i] = buildstore.Step{Name: n, Status: "queued"}
	}
	setStep := func(i int, status, conclusion string, progress int) {
		steps[i].Status, steps[i].Conclusion = status, conclusion
		s.store.Update(p.ID, func(b *buildstore.Build) {
			b.Status = buildstore.StatusBuilding
			b.Steps = append([]buildstore.Step(nil), steps...)
			b.CurrentStep = steps[i].Name
			if progress > b.Progress {
				b.Progress = progress
			}
		})
	}

	// 1. Projet temporaire : copie du template + injection de la config.
	setStep(0, "in_progress", "", 2)
	work, err := os.MkdirTemp("", "apk-"+p.ID+"-")
	if err != nil {
		s.fail(p.ID, "dossier de travail impossible: "+err.Error())
		return
	}
	defer os.RemoveAll(work)
	if err := prepareProject(s.cfg.TemplateDir, work, p, s.cfg.AndroidHome); err != nil {
		s.fail(p.ID, "préparation du projet impossible: "+err.Error())
		return
	}
	setStep(0, "completed", "success", 10)

	// 2. Gradle. La progression est estimée au nombre de tâches exécutées.
	setStep(1, "in_progress", "", 10)
	cmd := s.gradleCommand(ctx, work)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		s.fail(p.ID, "lancement de Gradle impossible: "+err.Error())
		return
	}
	var tail ring
	done := make(chan struct{})
	go func() {
		defer close(done)
		tasks := 0
		buf := make([]byte, 4096)
		var line []byte
		for {
			n, err := pr.Read(buf)
			for _, c := range buf[:n] {
				if c != '\n' {
					line = append(line, c)
					continue
				}
				l := string(line)
				line = line[:0]
				tail.add(l)
				if strings.HasPrefix(l, "> Task ") {
					tasks++
					// ~40 tâches pour un assembleDebug : plafonné à 94 %.
					pct := 10 + tasks*84/40
					if pct > 94 {
						pct = 94
					}
					s.store.Update(p.ID, func(b *buildstore.Build) {
						if pct > b.Progress {
							b.Progress = pct
						}
					})
				}
			}
			if err != nil {
				return
			}
		}
	}()
	werr := cmd.Wait()
	pw.Close()
	<-done
	if werr != nil {
		setStep(1, "completed", "failure", 0)
		log.Error("gradle", "err", werr, "tail", tail.String())
		s.fail(p.ID, "compilation locale en échec: "+tail.lastLines(12))
		return
	}
	setStep(1, "completed", "success", 95)

	// 3. Récupérer l'APK dans releases/.
	setStep(2, "in_progress", "", 95)
	apk, err := os.ReadFile(filepath.Join(work, "app/build/outputs/apk/debug/app-debug.apk"))
	if err != nil {
		s.fail(p.ID, "APK introuvable après compilation: "+err.Error())
		return
	}
	b, _ := s.store.Get(p.ID)
	path := filepath.Join(s.cfg.Releases, fmt.Sprintf("%s-%s.apk", sanitize(b.AppName), p.ID))
	if err := os.WriteFile(path, apk, 0o644); err != nil {
		s.fail(p.ID, "écriture de l'APK impossible: "+err.Error())
		return
	}
	setStep(2, "completed", "success", 100)
	s.store.Update(p.ID, func(b *buildstore.Build) { b.CurrentStep = "" })
	s.store.SetAPKPath(p.ID, path)
	log.Info("APK écrit", "chemin", path, "taille", len(apk))
}

// gradleCommand : GRADLE_BIN s'il est défini, sinon le ./gradlew du template
// (Gradle 8.7, compatible avec le plugin Android 8.5.2 utilisé).
func (s *Server) gradleCommand(ctx context.Context, work string) *exec.Cmd {
	args := []string{"assembleDebug", "--build-cache", "--console=plain", "--no-daemon"}
	bin := "./gradlew"
	if s.cfg.GradleBin != "" {
		bin = s.cfg.GradleBin
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "ANDROID_HOME="+s.cfg.AndroidHome, "ANDROID_SDK_ROOT="+s.cfg.AndroidHome)
	return cmd
}

// prepareProject copie le template dans dst et applique la même configuration
// que le workflow GitHub (applicationId, versions, strings, splash, icône, dist).
func prepareProject(tmpl, dst string, p localParams, sdk string) error {
	if err := copyTree(tmpl, dst); err != nil {
		return err
	}
	gradlePath := filepath.Join(dst, "app/build.gradle.kts")
	g, err := os.ReadFile(gradlePath)
	if err != nil {
		return err
	}
	gs := strings.NewReplacer(
		"__APPLICATION_ID__", p.Package,
		"__VERSION_NAME__", p.VersionName,
		"__VERSION_CODE__", p.VersionCode,
	).Replace(string(g))
	if err := os.WriteFile(gradlePath, []byte(gs), 0o644); err != nil {
		return err
	}

	b := func(v bool) string { return boolStr(v) }
	strs := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string name="app_name">%s</string>
    <string name="app_url">%s</string>
    <bool name="hide_scrollbar">%s</bool>
    <bool name="debug_console">%s</bool>
    <bool name="fix_images">%s</bool>
    <bool name="background_player">%s</bool>
    <bool name="picture_in_picture">%s</bool>
</resources>
`, html.EscapeString(p.AppName), html.EscapeString(p.AppURL),
		b(p.HideScrollbar), b(p.DebugConsole), b(p.FixImages), b(p.BackgroundPlayer), b(p.PictureInPicture))
	res := filepath.Join(dst, "app/src/main/res")
	if err := os.WriteFile(filepath.Join(res, "values/strings.xml"), []byte(strs), 0o644); err != nil {
		return err
	}
	colors := fmt.Sprintf("<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<resources>\n    <color name=\"splash_bg\">%s</color>\n</resources>\n", p.SplashBg)
	if err := os.WriteFile(filepath.Join(res, "values/colors.xml"), []byte(colors), 0o644); err != nil {
		return err
	}
	if p.Icon != nil {
		if err := os.WriteFile(filepath.Join(res, "mipmap-xxxhdpi/ic_launcher.png"), p.Icon, 0o644); err != nil {
			return err
		}
	}
	if p.Dist != nil {
		if err := embedDist(p.Dist, filepath.Join(dst, "app/src/main/assets/www")); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(dst, "local.properties"), []byte("sdk.dir="+sdk+"\n"), 0o644)
}

// copyTree copie un dossier en ignorant les sorties de build.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if n := d.Name(); n == "build" || n == ".gradle" || n == ".idea" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if d.Name() == "local.properties" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), data, info.Mode().Perm())
	})
}

// embedDist extrait le zip dans dst, à partir du dossier qui contient index.html
// (gère un éventuel dossier racine). Refuse les chemins qui sortent de dst.
func embedDist(data []byte, dst string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	root, found := "", false
	for _, f := range zr.File {
		if !f.FileInfo().IsDir() && strings.EqualFold(filepath.Base(f.Name), "index.html") {
			if d := filepath.Dir(f.Name); !found || len(d) < len(root) {
				root, found = d, true
			}
		}
	}
	if !found {
		return fmt.Errorf("index.html introuvable dans le dist")
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, f := range zr.File {
		rel := f.Name
		if root != "." {
			var ok bool
			if rel, ok = strings.CutPrefix(rel, root+"/"); !ok {
				continue
			}
		}
		target := filepath.Join(dst, filepath.FromSlash(rel))
		if target != dst && !strings.HasPrefix(target, dst+string(os.PathSeparator)) {
			return fmt.Errorf("chemin interdit dans le zip: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(target)
		if err == nil {
			_, err = io.Copy(out, rc)
			out.Close()
		}
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// ring garde les dernières lignes de sortie de Gradle (pour les messages d'erreur).
type ring struct{ lines []string }

func (r *ring) add(l string) {
	r.lines = append(r.lines, l)
	if len(r.lines) > 60 {
		r.lines = r.lines[len(r.lines)-60:]
	}
}
func (r *ring) String() string { return strings.Join(r.lines, "\n") }
func (r *ring) lastLines(n int) string {
	l := r.lines
	if len(l) > n {
		l = l[len(l)-n:]
	}
	return strings.Join(l, "\n")
}
