// Package web embeds the built web/static tree into the server binary.
package web

import (
	"embed"
	"io/fs"
)

// StaticFS embeds everything under static/ actually served to the browser.
// codemirror6's node_modules/build.mjs/package.json/bun.lock (~102MB) are
// deliberately left off this list: build-time tooling, never requested at
// runtime, and go:embed has no exclude syntax — only cm6.bundle.js/chunks/
// entry.js (what the built bundle references) are embedded.
//
//go:embed static/*.html static/css static/js static/help static/favicon.ico static/manifest.json
//go:embed static/vendor/xterm static/vendor/bootstrap static/vendor/sweetalert2
//go:embed static/vendor/codemirror static/vendor/tabler static/vendor/jquery
//go:embed static/vendor/fontawesome static/vendor/marked static/vendor/dompurify
//go:embed static/vendor/summernote static/vendor/fonts
//go:embed static/vendor/codemirror6/cm6.bundle.js static/vendor/codemirror6/chunks static/vendor/codemirror6/entry.js
var StaticFS embed.FS

// Static returns StaticFS rooted at "static" so paths match the URL space
// (no "static/" prefix) exactly like http.Dir("web/static") did.
func Static() fs.FS {
	sub, err := fs.Sub(StaticFS, "static")
	if err != nil {
		panic(err) // static/ is embedded at build time; a missing dir here is a build bug, not a runtime condition
	}
	return sub
}
