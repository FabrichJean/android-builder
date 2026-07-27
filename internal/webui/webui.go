// Package webui embarque l'interface web statique dans le binaire.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed index.html style.css app.js
var files embed.FS

// FS renvoie le système de fichiers contenant l'UI (HTML, CSS, JS).
func FS() fs.FS { return files }
