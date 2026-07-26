// Package thumb génère des miniatures (screenshots) via un Chrome headless local.
// Il atteint donc les URL publiques ET locales (LAN), et rend un dist embarqué
// en le servant sur un serveur statique local.
package thumb

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func init() {
	// MIME corrects pour que Chrome rende bien un dist servi localement.
	_ = mime.AddExtensionType(".js", "text/javascript")
	_ = mime.AddExtensionType(".mjs", "text/javascript")
	_ = mime.AddExtensionType(".wasm", "application/wasm")
	_ = mime.AddExtensionType(".css", "text/css")
	_ = mime.AddExtensionType(".json", "application/json")
}

// ChromePath localise un binaire Chrome/Chromium, ou "" si introuvable.
func ChromePath() string {
	if p := os.Getenv("CHROME_PATH"); p != "" {
		return p
	}
	for _, c := range []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
		"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
	} {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	for _, n := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "brave-browser", "microsoft-edge"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}

// CaptureURL screenshote une URL vers outPath (PNG).
func CaptureURL(ctx context.Context, chrome, url, outPath string) error {
	return run(ctx, chrome, url, outPath)
}

// CaptureDist décompresse un dist (zip), le sert localement et le screenshote.
func CaptureDist(ctx context.Context, chrome string, distZip []byte, outPath string) error {
	dir, err := os.MkdirTemp("", "apk-dist-shot-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := unzip(distZip, dir); err != nil {
		return err
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: http.FileServer(http.Dir(indexDir(dir)))}
	go srv.Serve(ln)
	defer srv.Close()

	url := fmt.Sprintf("http://%s/index.html", ln.Addr().String())
	return run(ctx, chrome, url, outPath)
}

func run(ctx context.Context, chrome, url, outPath string) error {
	if chrome == "" {
		return errors.New("chrome introuvable")
	}
	ud, err := os.MkdirTemp("", "apk-chrome-ud-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(ud)
	_ = os.Remove(outPath) // repartir propre

	// headless=old : mode « one-shot » qui écrit le screenshot puis quitte.
	cmd := exec.Command(chrome,
		"--headless=old", "--disable-gpu", "--hide-scrollbars", "--mute-audio",
		"--no-sandbox", "--no-first-run", "--no-default-browser-check",
		"--disable-extensions", "--disable-background-networking", "--disable-component-update",
		"--user-data-dir="+ud,
		"--window-size=1000,650",
		"--force-device-scale-factor=1",
		"--virtual-time-budget=9000",
		"--screenshot="+outPath,
		url,
	)
	// Stdout/Stderr laissés nil -> /dev/null (pas de pipe, donc on n'attend pas
	// les petits-enfants comme GoogleUpdater qui feraient blocage).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) // tue le groupe (chrome + enfants)
		_ = cmd.Wait()
	}()

	// On attend l'apparition du screenshot plutôt que la fin de Chrome.
	deadline := time.Now().Add(25 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	for {
		if fi, statErr := os.Stat(outPath); statErr == nil && fi.Size() > 0 {
			time.Sleep(250 * time.Millisecond) // laisse l'écriture se terminer
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("timeout screenshot")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func unzip(data []byte, dst string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := filepath.Clean(f.Name)
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) { // anti zip-slip
			continue
		}
		target := filepath.Join(dst, name)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(target)
		if err != nil {
			rc.Close()
			return err
		}
		_, cErr := io.Copy(out, rc)
		out.Close()
		rc.Close()
		if cErr != nil {
			return cErr
		}
	}
	return nil
}

// indexDir renvoie le dossier le moins profond contenant index.html.
func indexDir(root string) string {
	best, bestDepth := root, 1<<30
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.EqualFold(d.Name(), "index.html") {
			if depth := strings.Count(p, string(os.PathSeparator)); depth < bestDepth {
				bestDepth, best = depth, filepath.Dir(p)
			}
		}
		return nil
	})
	return best
}
