// Package webassets contains the dashboard bundled into the dotshift binary.
package webassets

import (
	"embed"
	"io/fs"
)

//go:embed dist
var assets embed.FS

// Files returns the compiled dashboard. Regenerate it with `make web`.
func Files() fs.FS {
	files, _ := fs.Sub(assets, "dist")
	return files
}
