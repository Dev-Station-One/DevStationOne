package api

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"devstation/internal/database"
	"devstation/internal/filesystem"
	"devstation/internal/protocol"
	"devstation/internal/runtimes"
)

// fileRPCTimeout bounds how long a files REST request waits for the runtime
// to perform the operation and reply, including a transfer of up to
// protocol.MaxFileBytes over the control-plane connection.
const fileRPCTimeout = 30 * time.Second

// fileCommand issues a file.command RPC to the project's runtime and decodes
// its result, writing an appropriate error response on failure.
func fileCommand(w http.ResponseWriter, r *http.Request, conn runtimes.Conn, cmd protocol.FileCommandPayload) (protocol.FileResultPayload, bool) {
	ctx, cancel := context.WithTimeout(r.Context(), fileRPCTimeout)
	defer cancel()

	raw, err := conn.SendCommand(ctx, protocol.MessageFileCommand, cmd)
	if err != nil {
		if ctx.Err() != nil {
			writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "timed out waiting for runtime"})
		} else {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		}
		return protocol.FileResultPayload{}, false
	}

	var result protocol.FileResultPayload
	if err := json.Unmarshal(raw, &result); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "invalid response from runtime"})
		return protocol.FileResultPayload{}, false
	}
	return result, true
}

// fileRoot validates the caller against the project, returns the runtime
// connection and the containment root (the Project's working directory),
// writing an error response and returning ok=false on failure.
func fileRoot(w http.ResponseWriter, r *http.Request, db *database.DB, registry *runtimes.Registry) (runtimes.Conn, string, bool) {
	conn, project, ok := runtimeConnForProject(w, r, db, registry, r.PathValue("projectID"))
	if !ok {
		return nil, "", false
	}
	return conn, project.WorkingDirectory, true
}

// cleanQueryParam validates a ?path= style query parameter as a
// root-relative path, writing a 400 response and returning ok=false when it
// is empty or escapes the root. The Control Server-side check is an early
// rejection only; the runtime re-checks containment (docs/SECURITY.md).
func cleanQueryParam(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	p, err := filesystem.Clean(r.URL.Query().Get(name))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return "", false
	}
	return p, true
}

// decodeFileBody decodes a {"path": ...}/{from, to} JSON request body and
// validates every path in it, writing a 400 response and returning ok=false
// on failure.
func decodeFileBody(w http.ResponseWriter, r *http.Request) (from, to string, ok bool) {
	var body struct {
		Path string `json:"path"`
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return "", "", false
	}

	clean := func(p string) (string, bool) {
		c, err := filesystem.Clean(p)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return "", false
		}
		return c, true
	}

	switch {
	case body.Path != "" && body.To != "":
		if from, ok = clean(body.Path); !ok {
			return "", "", false
		}
		if to, ok = clean(body.To); !ok {
			return "", "", false
		}
		return from, to, true
	case body.Path != "":
		if from, ok = clean(body.Path); !ok {
			return "", "", false
		}
		return from, "", true
	case body.From != "":
		if from, ok = clean(body.From); !ok {
			return "", "", false
		}
		if to, ok = clean(body.To); !ok {
			return "", "", false
		}
		return from, to, true
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path is required"})
		return "", "", false
	}
}

// listFilesHandler GET .../files?path= — list a directory.
func listFilesHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, root, ok := fileRoot(w, r, db, registry)
		if !ok {
			return
		}
		p, ok := cleanQueryParam(w, r, "path")
		if !ok {
			return
		}
		result, ok := fileCommand(w, r, conn, protocol.FileCommandPayload{Action: "list", Root: root, Path: p})
		if !ok {
			return
		}
		if result.Entries == nil {
			result.Entries = []protocol.FileInfo{}
		}
		writeJSON(w, http.StatusOK, result.Entries)
	}
}

// statFileHandler GET .../files/stat?path= — stat one entry.
func statFileHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, root, ok := fileRoot(w, r, db, registry)
		if !ok {
			return
		}
		p, ok := cleanQueryParam(w, r, "path")
		if !ok {
			return
		}
		result, ok := fileCommand(w, r, conn, protocol.FileCommandPayload{Action: "stat", Root: root, Path: p})
		if !ok {
			return
		}
		if result.Info == nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "runtime returned no file info"})
			return
		}
		writeJSON(w, http.StatusOK, result.Info)
	}
}

// readFileHandler GET .../files/content?path=&download=1 — stream a file's
// raw bytes to the browser.
func readFileHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, root, ok := fileRoot(w, r, db, registry)
		if !ok {
			return
		}
		p, ok := cleanQueryParam(w, r, "path")
		if !ok {
			return
		}
		result, ok := fileCommand(w, r, conn, protocol.FileCommandPayload{Action: "read", Root: root, Path: p})
		if !ok {
			return
		}

		ctype := contentTypeFor(p)
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// HTML/SVG bodies served inline on this origin would execute scripts
		// on direct navigation, so force a download for them. The editor and
		// <img> previews fetch the bytes and ignore the disposition.
		if r.URL.Query().Get("download") == "1" || renderableOnOrigin(ctype) {
			w.Header().Set("Content-Disposition", "attachment; filename=\""+downloadName(p)+"\"")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(result.Data)
	}
}

// writeFileHandler PUT .../files/content?path= — write/upload a file; the
// raw request body is the file.
func writeFileHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, root, ok := fileRoot(w, r, db, registry)
		if !ok {
			return
		}
		p, ok := cleanQueryParam(w, r, "path")
		if !ok {
			return
		}
		if p == "." {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path must name a file"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, protocol.MaxFileBytes)
		data, err := io.ReadAll(r.Body)
		if err != nil {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
				"error": "file exceeds the " + strconv.Itoa(int(protocol.MaxFileBytes>>20)) + " MB upload limit",
			})
			return
		}
		if _, ok := fileCommand(w, r, conn, protocol.FileCommandPayload{Action: "write", Root: root, Path: p, Data: data}); !ok {
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "written"})
	}
}

// createFileHandler POST .../files/create {"path"} — create an empty file.
func createFileHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return fileMutateHandler(db, registry, "create", true)
}

// mkdirHandler POST .../files/mkdir {"path"} — create a directory.
func mkdirHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return fileMutateHandler(db, registry, "mkdir", false)
}

// fileMutateHandler builds a handler for a file.action that takes exactly
// one path in the request body and replies 204.
func fileMutateHandler(db *database.DB, registry *runtimes.Registry, action string, requireFile bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, root, ok := fileRoot(w, r, db, registry)
		if !ok {
			return
		}
		p, _, ok := decodeFileBody(w, r)
		if !ok {
			return
		}
		if requireFile && p == "." {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path must name a file"})
			return
		}
		if !requireFile && p == "." {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path must name a directory"})
			return
		}
		if _, ok := fileCommand(w, r, conn, protocol.FileCommandPayload{Action: action, Root: root, Path: p}); !ok {
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// fileTransferHandler builds a handler for rename/copy/move {"from","to"}.
func fileTransferHandler(db *database.DB, registry *runtimes.Registry, action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, root, ok := fileRoot(w, r, db, registry)
		if !ok {
			return
		}
		from, to, ok := decodeFileBody(w, r)
		if !ok {
			return
		}
		if _, ok := fileCommand(w, r, conn, protocol.FileCommandPayload{Action: action, Root: root, Path: from, To: to}); !ok {
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// deleteFileHandler DELETE .../files?path= — delete a file or directory
// tree.
func deleteFileHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, root, ok := fileRoot(w, r, db, registry)
		if !ok {
			return
		}
		p, ok := cleanQueryParam(w, r, "path")
		if !ok {
			return
		}
		if p == "." {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot delete the project root"})
			return
		}
		if _, ok := fileCommand(w, r, conn, protocol.FileCommandPayload{Action: "delete", Root: root, Path: p}); !ok {
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// searchFilesHandler GET .../files/search?q=&path= — name-substring search.
func searchFilesHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, root, ok := fileRoot(w, r, db, registry)
		if !ok {
			return
		}
		p, ok := cleanQueryParam(w, r, "path")
		if !ok {
			return
		}
		q := r.URL.Query().Get("q")
		if q == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "q is required"})
			return
		}
		result, ok := fileCommand(w, r, conn, protocol.FileCommandPayload{Action: "search", Root: root, Path: p, Query: q})
		if !ok {
			return
		}
		if result.Matches == nil {
			result.Matches = []string{}
		}
		writeJSON(w, http.StatusOK, result.Matches)
	}
}

func contentTypeFor(p string) string {
	if ct := mime.TypeByExtension(path.Ext(p)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// renderableOnOrigin reports whether a content type would execute script or
// render as a document if served inline on the DevStation origin.
func renderableOnOrigin(ct string) bool {
	base, _, _ := mime.ParseMediaType(ct)
	switch base {
	case "text/html", "image/svg+xml", "application/xhtml+xml", "application/xml", "text/xml":
		return true
	}
	return false
}

// downloadName derives a safe Content-Disposition filename from a
// root-relative path.
func downloadName(p string) string {
	name := path.Base(p)
	var b strings.Builder
	for _, r := range name {
		if r == '"' || r == '\\' || r < 0x20 || r == 0x7f {
			b.WriteRune('_')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
