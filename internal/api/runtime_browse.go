package api

import (
	"net/http"

	"devstationone/internal/database"
	"devstationone/internal/protocol"
	"devstationone/internal/runtimes"
)

// browseRuntimeHandler is GET .../runtimes/{runtimeID}/browse?path= — list a
// directory under the runtime's home directory, for the Project setup UI's
// directory picker (there is no Project yet at this point, so file.command
// is issued with Root = the runtime's home dir instead of a Project's
// working directory — the same RPC and containment the file manager uses).
// Any signed-in session may browse any runtime, matching the existing
// visibility of GET /api/v1/runtimes (runtimes are shared infrastructure,
// not owned per-user).
func browseRuntimeHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rt, conn, ok := resolveRuntime(w, r, db, registry, r.PathValue("runtimeID"))
		if !ok {
			return
		}
		p, ok := cleanQueryParam(w, r, "path")
		if !ok {
			return
		}
		result, ok := fileCommand(w, r, conn, protocol.FileCommandPayload{Action: "list", Root: rt.Capabilities.HomeDir, Path: p})
		if !ok {
			return
		}
		if result.Entries == nil {
			result.Entries = []protocol.FileInfo{}
		}
		writeJSON(w, http.StatusOK, result.Entries)
	}
}

// browseMkdirHandler is POST .../runtimes/{runtimeID}/browse/mkdir — create a
// new directory under the runtime's home directory, for the picker's "new
// folder" action.
func browseMkdirHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rt, conn, ok := resolveRuntime(w, r, db, registry, r.PathValue("runtimeID"))
		if !ok {
			return
		}
		p, _, ok := decodeFileBody(w, r)
		if !ok {
			return
		}
		if p == "." {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path must name a directory"})
			return
		}
		if _, ok := fileCommand(w, r, conn, protocol.FileCommandPayload{Action: "mkdir", Root: rt.Capabilities.HomeDir, Path: p}); !ok {
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
