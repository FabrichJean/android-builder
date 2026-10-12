package appgen

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func TestParseProjectMultiFile(t *testing.T) {
	out := "=== FILE: index.html ===\n<!doctype html><html><body></body></html>\n=== FILE: css/style.css ===\n```css\nbody{margin:0}\n```\n=== FILE: js/main.js ===\nimport './ui.js';\n"
	files, err := parseProject(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 || files[0].Path != "index.html" {
		t.Fatalf("fichiers inattendus: %+v", files)
	}
	if files[1].Content != "body{margin:0}\n" {
		t.Fatalf("fences non retirées: %q", files[1].Content)
	}
	zipped, err := zipFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(zipped), int64(len(zipped)))
	if err != nil || len(zr.File) != 3 {
		t.Fatalf("zip invalide: %v", err)
	}
}

func TestParseProjectLegacySingleHTML(t *testing.T) {
	files, err := parseProject("blabla\n<!DOCTYPE html><html></html>\nfin")
	if err != nil || len(files) != 1 || files[0].Path != "index.html" {
		t.Fatalf("repli ancien format: %+v %v", files, err)
	}
}

func TestParseProjectRejects(t *testing.T) {
	doc := "<!doctype html><html></html>"
	idx := "=== FILE: index.html ===\n" + doc + "\n"
	cases := map[string]string{
		"traversée":        idx + "=== FILE: ../evil.js ===\nx\n",
		"absolu":           idx + "=== FILE: /etc/x.js ===\nx\n",
		"extension":        idx + "=== FILE: run.sh ===\nx\n",
		"caché":            idx + "=== FILE: .env ===\nx\n",
		"doublon":          idx + idx,
		"sans index":       "=== FILE: js/main.js ===\nx\n",
		"index non html":   "=== FILE: index.html ===\nhello\n",
		"trop de fichiers": idx + manyFiles(maxProjectFiles),
	}
	for name, out := range cases {
		if _, err := parseProject(out); err == nil {
			t.Errorf("%s : attendu une erreur", name)
		}
	}
}

func manyFiles(n int) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteString("=== FILE: js/m" + strings.Repeat("a", i+1) + ".js ===\nx\n")
	}
	return sb.String()
}
