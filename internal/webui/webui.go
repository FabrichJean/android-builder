// Package webui embarque l'interface web statique dans le binaire.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed index.html
var files embed.FS

// FS renvoie le système de fichiers contenant l'UI (index.html).
func FS() fs.FS { return files }
