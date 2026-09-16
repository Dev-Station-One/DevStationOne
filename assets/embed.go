// Package assets embeds the brand assets (logos, social preview image)
// served under /assets/ into the server binary.
package assets

import "embed"

//go:embed *
var FS embed.FS
