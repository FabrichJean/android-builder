package appgen

import (
	"archive/zip"
	"bytes"
	"fmt"
	"regexp"
	"strings"
)

// Le modèle renvoie un PROJET (plusieurs fichiers) dans un format texte
// simple : chaque fichier est précédé d'une ligne "=== FILE: chemin ===".
// parseProject valide strictement ces chemins avant tout empaquetage : la
// sortie du modèle est une donnée non fiable (chemins, extensions, taille).

const (
	maxProjectFiles = 12
	maxFileBytes    = 400 << 10
)

var (
	fileMarkerRe = regexp.MustCompile(`(?m)^=== FILE: (.+?) ===[ \t]*\r?$`)
	// Chemin relatif simple : segments [a-z0-9._-], pas de ".." ni de dossier caché.
	filePathRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*(\.[a-z0-9_-]+)*(/[a-z0-9][a-z0-9_-]*(\.[a-z0-9_-]+)*)*\.[a-z0-9]+$`)
	allowedExts  = map[string]bool{".html": true, ".css": true, ".js": true, ".json": true, ".svg": true, ".txt": true, ".md": true}
	fenceStartRe = regexp.MustCompile("^\\s*```[a-zA-Z]*\\s*\n")
	fenceEndRe   = regexp.MustCompile("\n\\s*```\\s*$")
)

// File est un fichier du projet généré.
type File struct {
	Path    string
	Content string
}

// parseProject extrait les fichiers de la sortie du modèle. Sans marqueur, un
// document HTML complet est accepté comme projet à un seul fichier (ancien
// format, repli). index.html est obligatoire.
func parseProject(out string) ([]File, error) {
	locs := fileMarkerRe.FindAllStringSubmatchIndex(out, -1)
	if len(locs) == 0 {
		doc, ok := extractHTML(out)
		if !ok {
			return nil, fmt.Errorf("la sortie générée n'est pas un projet web (aucun fichier reconnu)")
		}
		return []File{{Path: "index.html", Content: doc}}, nil
	}
	if len(locs) > maxProjectFiles {
		return nil, fmt.Errorf("projet trop gros : %d fichiers (max %d)", len(locs), maxProjectFiles)
	}

	var files []File
	seen := map[string]bool{}
	for i, loc := range locs {
		path := strings.TrimSpace(out[loc[2]:loc[3]])
		end := len(out)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		body := out[loc[1]:end]
		body = strings.TrimPrefix(body, "\n")
		body = fenceStartRe.ReplaceAllString(body, "")
		body = fenceEndRe.ReplaceAllString(strings.TrimRight(body, "\n \t"), "")
		body = strings.TrimSpace(body) + "\n"

		if !filePathRe.MatchString(path) {
			return nil, fmt.Errorf("chemin de fichier refusé : %q", path)
		}
		if ext := path[strings.LastIndex(path, "."):]; !allowedExts[ext] {
			return nil, fmt.Errorf("type de fichier refusé : %q", path)
		}
		if seen[path] {
			return nil, fmt.Errorf("fichier en double : %q", path)
		}
		seen[path] = true
		if len(body) > maxFileBytes {
			return nil, fmt.Errorf("fichier trop volumineux : %q", path)
		}
		files = append(files, File{Path: path, Content: body})
	}

	for _, f := range files {
		if f.Path == "index.html" {
			if !strings.Contains(strings.ToLower(f.Content), "<!doctype") {
				return nil, fmt.Errorf("index.html n'est pas un document HTML complet")
			}
			return files, nil
		}
	}
	return nil, fmt.Errorf("index.html manquant dans le projet généré")
}

// zipFiles fabrique en mémoire le dist.zip du projet (index.html à la racine).
func zipFiles(files []File) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		w, err := zw.Create(f.Path)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(f.Content)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
