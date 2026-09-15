package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"devstationone/internal/audit"
	"devstationone/internal/database"
	"devstationone/internal/projects"
	"devstationone/internal/protocol"
	"devstationone/internal/runtimes"
	"devstationone/internal/services"
)

// serviceRPCTimeout bounds how long a service REST request waits for the
// runtime to run the underlying systemctl/journalctl command and reply.
const serviceRPCTimeout = 15 * time.Second

// maxLogLines caps the ?lines= query parameter on the logs endpoint.
const maxLogLines = 2000

// runtimeConnForProject resolves projectID's runtime connection for
// project-scoped operations (files, docker, editor streams) — distinct from
// runtime-level services, which resolve by runtime ID directly (ADR-043).
func runtimeConnForProject(w http.ResponseWriter, r *http.Request, db *database.DB, registry *runtimes.Registry, projectID string) (runtimes.Conn, projects.Project, bool) {
	project, ok := authorizedProject(w, r, db, projectID)
	if !ok {
		return nil, projects.Project{}, false
	}
	conn, online := registry.Get(project.RuntimeID)
	if !online {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "runtime is offline"})
		return nil, projects.Project{}, false
	}
	return conn, project, true
}

// serviceCommand issues a service.command RPC to a runtime and decodes its
// result, writing an appropriate error response on failure. Services are
// runtime-host resources (ADR-043): they resolve by runtime ID directly, not
// through a project.
func serviceCommand(w http.ResponseWriter, r *http.Request, conn runtimes.Conn, cmd protocol.ServiceCommandPayload) (protocol.ServiceResultPayload, bool) {
	ctx, cancel := context.WithTimeout(r.Context(), serviceRPCTimeout)
	defer cancel()

	raw, err := conn.SendCommand(ctx, protocol.MessageServiceCommand, cmd)
	if err != nil {
		if ctx.Err() != nil {
			writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "timed out waiting for runtime"})
		} else {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		}
		return protocol.ServiceResultPayload{}, false
	}

	var result protocol.ServiceResultPayload
	if err := json.Unmarshal(raw, &result); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "invalid response from runtime"})
		return protocol.ServiceResultPayload{}, false
	}
	return result, true
}

func listServicesHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, conn, ok := resolveRuntime(w, r, db, registry, r.PathValue("runtimeID"))
		if !ok {
			return
		}
		result, ok := serviceCommand(w, r, conn, protocol.ServiceCommandPayload{Action: "list"})
		if !ok {
			return
		}
		if result.Services == nil {
			result.Services = []protocol.ServiceInfo{}
		}
		writeJSON(w, http.StatusOK, result.Services)
	}
}

// authorizedUnit resolves the {runtimeID} path value and validates the
// {unit} path value, writing an error response and returning ok=false on
// failure.
func authorizedUnit(w http.ResponseWriter, r *http.Request, db *database.DB, registry *runtimes.Registry) (runtimes.Conn, string, bool) {
	_, conn, ok := resolveRuntime(w, r, db, registry, r.PathValue("runtimeID"))
	if !ok {
		return nil, "", false
	}
	unit := r.PathValue("unit")
	if !services.ValidUnitName(unit) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid unit name"})
		return nil, "", false
	}
	return conn, unit, true
}

func getServiceHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, unit, ok := authorizedUnit(w, r, db, registry)
		if !ok {
			return
		}
		result, ok := serviceCommand(w, r, conn, protocol.ServiceCommandPayload{Action: "status", Unit: unit})
		if !ok {
			return
		}
		if result.Service == nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "runtime returned no service info"})
			return
		}
		writeJSON(w, http.StatusOK, result.Service)
	}
}

// serviceActionHandler builds a handler for a service action (start, stop,
// restart, enable, disable) that requires no request body and returns the
// unit's resulting status.
func serviceActionHandler(db *database.DB, registry *runtimes.Registry, action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, unit, ok := authorizedUnit(w, r, db, registry)
		if !ok {
			return
		}
		result, ok := serviceCommand(w, r, conn, protocol.ServiceCommandPayload{Action: action, Unit: unit})
		if !ok {
			return
		}
		if result.Service == nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "runtime returned no service info"})
			return
		}
		user, _ := userFromContext(r.Context())
		audit.Record(r.Context(), db.Pool, audit.EventServiceAction, audit.Actor{UserID: user.ID, Email: user.Email}, "service", unit, map[string]any{"action": action, "runtime_id": r.PathValue("runtimeID")}, clientIP(r))
		writeJSON(w, http.StatusOK, result.Service)
	}
}

func getServiceLogsHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, unit, ok := authorizedUnit(w, r, db, registry)
		if !ok {
			return
		}
		lines := 200
		if v := r.URL.Query().Get("lines"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "lines must be a positive integer"})
				return
			}
			if n > maxLogLines {
				n = maxLogLines
			}
			lines = n
		}
		result, ok := serviceCommand(w, r, conn, protocol.ServiceCommandPayload{Action: "logs", Unit: unit, Lines: lines})
		if !ok {
			return
		}
		if result.Logs == nil {
			result.Logs = []string{}
		}
		writeJSON(w, http.StatusOK, result.Logs)
	}
}
