package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"devstationone/internal/database"
	"devstationone/internal/dbconnections"
	"devstationone/internal/projects"
	"devstationone/internal/protocol"
	"devstationone/internal/runtimes"
)

// dbRPCTimeout bounds how long a database REST request waits for the
// runtime to open/ping a connection and reply.
const dbRPCTimeout = 30 * time.Second

type databaseConnectionRequest struct {
	Name         string `json:"name"`
	Engine       string `json:"engine"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	Username     string `json:"username"`
	Database     string `json:"database"`
	SSLMode      string `json:"ssl_mode"`
	InstanceName string `json:"instance_name"`
	SearchPath   string `json:"search_path"`
	// Password/ExtraParams are only accepted on create, and on a patch that
	// includes a "secret" object (see databaseConnectionPatchRequest).
	Password    string `json:"password"`
	ExtraParams string `json:"extra_params"`
}

type databaseConnectionPatchRequest struct {
	Name         *string `json:"name"`
	Engine       *string `json:"engine"`
	Host         *string `json:"host"`
	Port         *int    `json:"port"`
	Username     *string `json:"username"`
	Database     *string `json:"database"`
	SSLMode      *string `json:"ssl_mode"`
	InstanceName *string `json:"instance_name"`
	SearchPath   *string `json:"search_path"`
	// Secret is only set when the caller wants to replace the stored
	// password/extra-params; omitted (nil) keeps the existing secret.
	Secret *struct {
		Password    string `json:"password"`
		ExtraParams string `json:"extra_params"`
	} `json:"secret"`
}

type databaseConnectionResponse struct {
	ID           string    `json:"id"`
	ProjectID    string    `json:"project_id"`
	Name         string    `json:"name"`
	Engine       string    `json:"engine"`
	Host         string    `json:"host"`
	Port         int       `json:"port"`
	Username     string    `json:"username"`
	Database     string    `json:"database"`
	SSLMode      string    `json:"ssl_mode"`
	InstanceName string    `json:"instance_name"`
	SearchPath   string    `json:"search_path"`
	HasSecret    bool      `json:"has_secret"`
	CreatedBy    string    `json:"created_by"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func toDatabaseConnectionResponse(c dbconnections.Connection) databaseConnectionResponse {
	return databaseConnectionResponse{
		ID: c.ID, ProjectID: c.ProjectID, Name: c.Name, Engine: c.Engine, Host: c.Host, Port: c.Port,
		Username: c.Username, Database: c.Database, SSLMode: c.SSLMode, InstanceName: c.InstanceName,
		SearchPath: c.SearchPath, HasSecret: c.HasSecret, CreatedBy: c.CreatedBy,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

// validEngines are the database_connections.engine CHECK constraint values.
var validEngines = map[string]bool{"postgresql": true, "mysql": true, "mssql": true, "sqlite": true}

func validateConnectionFields(f dbconnections.Fields) string {
	switch {
	case strings.TrimSpace(f.Name) == "":
		return "name is required"
	case !validEngines[f.Engine]:
		return "engine must be one of: postgresql, mysql, mssql, sqlite"
	case f.Engine != "sqlite" && strings.TrimSpace(f.Host) == "":
		return "host is required"
	case strings.TrimSpace(f.Database) == "":
		if f.Engine == "sqlite" {
			return "database (file path) is required"
		}
		return "database is required"
	}
	return ""
}

func createDatabaseConnectionHandler(db *database.DB, key []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project, ok := authorizedProject(w, r, db, r.PathValue("projectID"))
		if !ok {
			return
		}
		user, _ := userFromContext(r.Context())

		var req databaseConnectionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		fields := dbconnections.Fields{
			Name: req.Name, Engine: req.Engine, Host: req.Host, Port: req.Port, Username: req.Username,
			Database: req.Database, SSLMode: req.SSLMode, InstanceName: req.InstanceName, SearchPath: req.SearchPath,
		}
		if msg := validateConnectionFields(fields); msg != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
			return
		}

		c, err := dbconnections.Create(r.Context(), db.Pool, project.ID, fields,
			dbconnections.SecretUpdate{Password: req.Password, ExtraParams: req.ExtraParams}, key, user.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create database connection"})
			return
		}
		writeJSON(w, http.StatusCreated, toDatabaseConnectionResponse(c))
	}
}

func listDatabaseConnectionsHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project, ok := authorizedProject(w, r, db, r.PathValue("projectID"))
		if !ok {
			return
		}
		list, err := dbconnections.ListForProject(r.Context(), db.Pool, project.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list database connections"})
			return
		}
		resp := make([]databaseConnectionResponse, 0, len(list))
		for _, c := range list {
			resp = append(resp, toDatabaseConnectionResponse(c))
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// authorizedConnection resolves the {connectionID} path value and confirms
// it belongs to the authorized {projectID}, writing a 404 (never leaking
// whether a connection exists under a different project) if not.
func authorizedConnection(w http.ResponseWriter, r *http.Request, db *database.DB) (dbconnections.Connection, bool) {
	project, ok := authorizedProject(w, r, db, r.PathValue("projectID"))
	if !ok {
		return dbconnections.Connection{}, false
	}
	c, err := dbconnections.Get(r.Context(), db.Pool, r.PathValue("connectionID"))
	if errors.Is(err, dbconnections.ErrNotFound) || c.ProjectID != project.ID {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "database connection not found"})
		return dbconnections.Connection{}, false
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to look up database connection"})
		return dbconnections.Connection{}, false
	}
	return c, true
}

func getDatabaseConnectionHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, ok := authorizedConnection(w, r, db)
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, toDatabaseConnectionResponse(c))
	}
}

// invalidateRuntimeCache best-effort notifies conn (if the runtime is
// online) to drop its cached connection pool for connectionID — sent after
// a connection's credentials change or it's deleted (ADR-021/022). Fire and
// forget: a delivery failure just means the runtime's cache expires on its
// own idle timeout instead of immediately, not a security hole (see
// docs/DECISIONS.md).
func invalidateRuntimeCache(registry *runtimes.Registry, runtimeID, connectionID string) {
	conn, online := registry.Get(runtimeID)
	if !online {
		return
	}
	_ = conn.SendEnvelope(protocol.MessageDBCommand, protocol.DBCommandPayload{Action: "invalidate", ConnectionID: connectionID})
}

func patchDatabaseConnectionHandler(db *database.DB, registry *runtimes.Registry, key []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		existing, ok := authorizedConnection(w, r, db)
		if !ok {
			return
		}
		var req databaseConnectionPatchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		fields := dbconnections.Fields{
			Name: existing.Name, Engine: existing.Engine, Host: existing.Host, Port: existing.Port,
			Username: existing.Username, Database: existing.Database, SSLMode: existing.SSLMode,
			InstanceName: existing.InstanceName, SearchPath: existing.SearchPath,
		}
		if req.Name != nil {
			fields.Name = *req.Name
		}
		if req.Engine != nil {
			fields.Engine = *req.Engine
		}
		if req.Host != nil {
			fields.Host = *req.Host
		}
		if req.Port != nil {
			fields.Port = *req.Port
		}
		if req.Username != nil {
			fields.Username = *req.Username
		}
		if req.Database != nil {
			fields.Database = *req.Database
		}
		if req.SSLMode != nil {
			fields.SSLMode = *req.SSLMode
		}
		if req.InstanceName != nil {
			fields.InstanceName = *req.InstanceName
		}
		if req.SearchPath != nil {
			fields.SearchPath = *req.SearchPath
		}
		if msg := validateConnectionFields(fields); msg != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
			return
		}

		var secret *dbconnections.SecretUpdate
		if req.Secret != nil {
			secret = &dbconnections.SecretUpdate{Password: req.Secret.Password, ExtraParams: req.Secret.ExtraParams}
		}

		c, err := dbconnections.Update(r.Context(), db.Pool, existing.ID, fields, secret, key)
		if errors.Is(err, dbconnections.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "database connection not found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update database connection"})
			return
		}
		if p, err := projects.Get(r.Context(), db.Pool, existing.ProjectID); err == nil {
			invalidateRuntimeCache(registry, p.RuntimeID, c.ID)
		}
		writeJSON(w, http.StatusOK, toDatabaseConnectionResponse(c))
	}
}

func deleteDatabaseConnectionHandler(db *database.DB, registry *runtimes.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		existing, ok := authorizedConnection(w, r, db)
		if !ok {
			return
		}
		ok, err := dbconnections.Delete(r.Context(), db.Pool, r.PathValue("connectionID"))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete database connection"})
			return
		}
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "database connection not found"})
			return
		}
		if p, err := projects.Get(r.Context(), db.Pool, existing.ProjectID); err == nil {
			invalidateRuntimeCache(registry, p.RuntimeID, existing.ID)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// dbConnRuntime resolves {connectionID}'s stored connection, its project's
// runtime connection, and its decrypted credentials, writing an appropriate
// error response and returning ok=false on any failure (offline runtime,
// missing connection, decrypt failure).
func dbConnRuntime(w http.ResponseWriter, r *http.Request, db *database.DB, registry *runtimes.Registry, key []byte) (runtimes.Conn, dbconnections.Connection, protocol.DBConnectionParams, bool) {
	c, ok := authorizedConnection(w, r, db)
	if !ok {
		return nil, dbconnections.Connection{}, protocol.DBConnectionParams{}, false
	}
	project, ok := authorizedProject(w, r, db, c.ProjectID)
	if !ok {
		return nil, dbconnections.Connection{}, protocol.DBConnectionParams{}, false
	}
	conn, online := registry.Get(project.RuntimeID)
	if !online {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "runtime is offline"})
		return nil, dbconnections.Connection{}, protocol.DBConnectionParams{}, false
	}
	params, err := dbconnections.ResolveParams(r.Context(), db.Pool, c.ID, key)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to resolve connection credentials"})
		return nil, dbconnections.Connection{}, protocol.DBConnectionParams{}, false
	}
	return conn, c, toDBConnectionParams(params), true
}

// dbCommand issues a db.command RPC to conn and decodes its result, writing
// an appropriate error response on failure — mirrors serviceCommand/
// fileCommand.
func dbCommand(w http.ResponseWriter, r *http.Request, conn runtimes.Conn, cmd protocol.DBCommandPayload) (protocol.DBResultPayload, bool) {
	ctx, cancel := context.WithTimeout(r.Context(), dbRPCTimeout)
	defer cancel()
	raw, err := conn.SendCommand(ctx, protocol.MessageDBCommand, cmd)
	if err != nil {
		if ctx.Err() != nil {
			writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "timed out waiting for runtime"})
		} else {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		}
		return protocol.DBResultPayload{}, false
	}
	var result protocol.DBResultPayload
	if err := json.Unmarshal(raw, &result); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "invalid response from runtime"})
		return protocol.DBResultPayload{}, false
	}
	return result, true
}

func testDatabaseConnectionHandler(db *database.DB, registry *runtimes.Registry, key []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, c, params, ok := dbConnRuntime(w, r, db, registry, key)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), dbRPCTimeout)
		defer cancel()
		raw, err := conn.SendCommand(ctx, protocol.MessageDBCommand, protocol.DBCommandPayload{
			Action: "test", ConnectionID: c.ID, Connection: params,
		})
		if err != nil {
			if ctx.Err() != nil {
				writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "timed out waiting for runtime"})
				return
			}
			// A db.error reply from the runtime (bad credentials, unreachable
			// host, ...) surfaces here as a normal, expected test failure —
			// not a server error.
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		var result protocol.DBResultPayload
		if err := json.Unmarshal(raw, &result); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "invalid response from runtime"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": result.Ok})
	}
}

func listSchemasHandler(db *database.DB, registry *runtimes.Registry, key []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, c, params, ok := dbConnRuntime(w, r, db, registry, key)
		if !ok {
			return
		}
		result, ok := dbCommand(w, r, conn, protocol.DBCommandPayload{Action: "schemas", ConnectionID: c.ID, Connection: params})
		if !ok {
			return
		}
		if result.Schemas == nil {
			result.Schemas = []string{}
		}
		writeJSON(w, http.StatusOK, result.Schemas)
	}
}

func listTablesHandler(db *database.DB, registry *runtimes.Registry, key []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, c, params, ok := dbConnRuntime(w, r, db, registry, key)
		if !ok {
			return
		}
		result, ok := dbCommand(w, r, conn, protocol.DBCommandPayload{
			Action: "tables", ConnectionID: c.ID, Connection: params, Schema: r.URL.Query().Get("schema"),
		})
		if !ok {
			return
		}
		if result.Tables == nil {
			result.Tables = []protocol.DBTableInfo{}
		}
		writeJSON(w, http.StatusOK, result.Tables)
	}
}

// tableStructureHandler builds a handler for one of the per-table structure
// actions (columns, indexes, constraints, triggers), all sharing the same
// {connectionID}/tables/{table}?schema= request shape.
func tableStructureHandler(db *database.DB, registry *runtimes.Registry, key []byte, action string, extract func(protocol.DBResultPayload) any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, c, params, ok := dbConnRuntime(w, r, db, registry, key)
		if !ok {
			return
		}
		result, ok := dbCommand(w, r, conn, protocol.DBCommandPayload{
			Action: action, ConnectionID: c.ID, Connection: params,
			Schema: r.URL.Query().Get("schema"), Table: r.PathValue("table"),
		})
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, extract(result))
	}
}

func toDBConnectionParams(p dbconnections.Params) protocol.DBConnectionParams {
	return protocol.DBConnectionParams{
		Engine: p.Engine, Host: p.Host, Port: p.Port, Username: p.Username, Password: p.Password,
		Database: p.Database, SSLMode: p.SSLMode, InstanceName: p.InstanceName, SearchPath: p.SearchPath,
		ExtraParams: p.ExtraParams,
	}
}
