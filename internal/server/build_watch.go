package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/example/android-builder/internal/buildstore"
	"github.com/example/android-builder/internal/ghclient"
)

// watch repère le run correspondant, suit son exécution puis récupère l'APK.
// cleanup (optionnel) est exécuté à la fin, quel que soit le résultat.
func (s *Server) watch(id, runName string, dispatchedAt time.Time, cleanup func()) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if cleanup != nil {
		defer cleanup()
	}

	log := s.log.With("build_id", id)

	// 1. Repérer le run déclenché (le run-name reprend le build_id).
	var run *ghclient.Run
	for {
		if ctx.Err() != nil {
			s.fail(id, "délai dépassé en attendant le démarrage du workflow")
			return
		}
		r, err := s.gh.FindRunByName(ctx, runName, dispatchedAt)
		if err != nil {
			log.Warn("find run", "err", err)
		} else if r != nil {
			run = r
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
	}

	s.store.Update(id, func(b *buildstore.Build) {
		b.Status = buildstore.StatusBuilding
		b.RunID = run.ID
		b.RunURL = run.HTMLURL
	})
	log.Info("run repéré", "run_id", run.ID, "url", run.HTMLURL)

	// 2. Attendre la fin du run en suivant les étapes en direct.
	for run.Status != "completed" {
		select {
		case <-ctx.Done():
			s.fail(id, "délai dépassé pendant le build")
			return
		case <-time.After(4 * time.Second):
		}

		// Étapes détaillées (progression fine).
		if steps, err := s.gh.GetSteps(ctx, run.ID); err == nil {
			s.store.Update(id, func(b *buildstore.Build) {
				applySteps(b, steps)
			})
		}

		r, err := s.gh.GetRun(ctx, run.ID)
		if err != nil {
			log.Warn("get run", "err", err)
			continue
		}
		run = r
	}

	if run.Conclusion != "success" {
		s.fail(id, "build en échec (conclusion: "+run.Conclusion+"), voir "+run.HTMLURL)
		return
	}

	// 3. Récupérer l'APK depuis l'artifact.
	apk, err := s.gh.DownloadAPK(ctx, run.ID)
	if err != nil {
		s.fail(id, "récupération de l'APK impossible: "+err.Error())
		return
	}

	// 4. Écrire l'APK dans le dossier releases/.
	b, _ := s.store.Get(id)
	filename := fmt.Sprintf("%s-%s.apk", sanitize(b.AppName), id)
	path := filepath.Join(s.cfg.Releases, filename)
	if err := os.WriteFile(path, apk, 0o644); err != nil {
		s.fail(id, "écriture de l'APK impossible: "+err.Error())
		return
	}
	s.store.Update(id, func(b *buildstore.Build) {
		b.Progress = 100
		b.CurrentStep = ""
	})
	s.store.SetAPKPath(id, path) // passe le statut à success en dernier
	log.Info("APK écrit", "chemin", path, "taille", len(apk))
}

// applySteps met à jour les étapes, la progression et l'étape courante d'un build.
func applySteps(b *buildstore.Build, steps []ghclient.Step) {
	if len(steps) == 0 {
		return
	}
	out := make([]buildstore.Step, 0, len(steps))
	completed, current := 0, ""
	for _, st := range steps {
		out = append(out, buildstore.Step{Name: st.Name, Status: st.Status, Conclusion: st.Conclusion})
		if st.Status == "completed" {
			completed++
		}
		if st.Status == "in_progress" && current == "" {
			current = st.Name
		}
	}
	b.Steps = out
	b.Progress = completed * 100 / len(steps)
	if current != "" {
		b.CurrentStep = current
	}
}

func (s *Server) fail(id, msg string) {
	s.log.Error("build échoué", "build_id", id, "msg", msg)
	s.store.Update(id, func(b *buildstore.Build) {
		b.Status = buildstore.StatusFailed
		b.Error = msg
	})
}
