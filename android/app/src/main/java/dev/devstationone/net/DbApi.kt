package dev.devstationone.net

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody.Companion.toRequestBody
import kotlinx.coroutines.suspendCancellableCoroutine
import okhttp3.Call
import okhttp3.Callback
import okhttp3.HttpUrl
import okhttp3.Response
import java.io.IOException
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

// ---- project databases (docs/DESIGN_ANDROID_DATABASES.md §4; live RPCs to the runtime) ----

/** One saved connection. The API never returns the secret, only [has_secret]. */
@Serializable
data class DbConnection(
    val id: String,
    val name: String,
    val engine: String, // postgresql | mysql | mssql | sqlite
    val host: String = "",
    val port: Int = 0,
    val username: String = "",
    val database: String = "",
    val ssl_mode: String = "",
    val instance_name: String = "",
    val search_path: String = "",
    val has_secret: Boolean = false,
)

@Serializable
data class DbTable(val name: String, val kind: String = "table") // table | view

@Serializable
data class DbColumn(
    val name: String,
    val type: String = "",
    val nullable: Boolean = true,
    val default: String = "",
    val position: Int = 0,
)

@Serializable
data class DbIndex(
    val name: String,
    val columns: List<String> = emptyList(),
    val unique: Boolean = false,
    val primary: Boolean = false,
)

@Serializable
data class DbConstraint(
    val name: String,
    val type: String = "", // primary_key | foreign_key | unique | check
    val columns: List<String> = emptyList(),
    val references: String = "",
)

@Serializable
data class DbTrigger(val name: String, val event: String = "", val timing: String = "")

/** A page of rows (rows/query answer): cells in [columns] order; no total count exists, only [has_more]. */
@Serializable
data class DbRows(
    val columns: List<String> = emptyList(),
    val rows: List<List<JsonElement>> = emptyList(),
    val has_more: Boolean = false,
    val is_exec: Boolean = false,
    val rows_affected: Long = 0,
)

/** Editable connection fields (everything but the secret). Blank [port] is sent as 0 = engine default. */
class DbConnForm(
    val name: String, val engine: String, val host: String, val port: Int, val username: String,
    val database: String, val ssl_mode: String, val instance_name: String, val search_path: String,
)

/** `/test` answers 200 with ok=false and the driver error for a bad connection — not an HTTP failure. */
@Serializable
data class DbTestResult(val ok: Boolean = false, val error: String = "")

/** A database-level user/role/login. */
@Serializable
data class DbPrincipal(val name: String, val can_login: Boolean = false, val superuser: Boolean = false)

/** One privilege; [raw] is set instead (and the rest blank) for engines that only expose native grant text (MySQL). */
@Serializable
data class DbGrant(val privilege: String = "", val schema: String = "", val table: String = "", val raw: String = "")

/** One backup row. Create is async: it starts as `running` and the list is polled until `done`/`failed`. */
@Serializable
data class DbBackup(
    val id: String,
    val file_name: String = "",
    val size_bytes: Long = 0,
    val trigger: String = "", // manual | scheduled | pre_restore
    val state: String = "", // running | done | failed
    val error: String = "",
    val created_at: String = "",
)

@Serializable
data class DbSchedule(
    val cadence: String = "", // hourly | daily | weekly
    val retention: Int = 0,
    val next_run_at: String = "",
    val last_run_at: String? = null,
    val last_result: String = "",
    val last_error: String = "",
)

/** One row change for the commit call: [original] = primary-key columns only (D3); [values] = changed columns. */
class DbEdit(val op: String, val original: Map<String, JsonElement> = emptyMap(), val values: Map<String, JsonElement> = emptyMap())

private fun Api.dbUrl(projectID: String, vararg segs: String): HttpUrl =
    url("api", "v1", "projects", projectID, "databases", *segs)

/** Schema param is omitted for "" (SQLite has none; the runtime then uses the engine default). */
private fun HttpUrl.withSchema(schema: String): HttpUrl =
    if (schema.isEmpty()) this else newBuilder().addQueryParameter("schema", schema).build()

private inline fun <reified T> Api.dbGet(url: HttpUrl): T =
    Api.client.newCall(request("GET", url)).execute().use {
        check(it)
        Api.json.decodeFromString<T>(it.jsonText())
    }

private fun Api.dbTableUrl(projectID: String, connID: String, schema: String, table: String, kind: String) =
    dbUrl(projectID, connID, "tables", table, kind).withSchema(schema)

fun Api.dbConnections(projectID: String): List<DbConnection> = dbGet(dbUrl(projectID))

fun Api.dbSchemas(projectID: String, connID: String): List<String> = dbGet(dbUrl(projectID, connID, "schemas"))

fun Api.dbTables(projectID: String, connID: String, schema: String): List<DbTable> =
    dbGet(dbUrl(projectID, connID, "tables").withSchema(schema))

fun Api.dbColumns(projectID: String, connID: String, schema: String, table: String): List<DbColumn> =
    dbGet(dbTableUrl(projectID, connID, schema, table, "columns"))

fun Api.dbIndexes(projectID: String, connID: String, schema: String, table: String): List<DbIndex> =
    dbGet(dbTableUrl(projectID, connID, schema, table, "indexes"))

fun Api.dbConstraints(projectID: String, connID: String, schema: String, table: String): List<DbConstraint> =
    dbGet(dbTableUrl(projectID, connID, schema, table, "constraints"))

fun Api.dbTriggers(projectID: String, connID: String, schema: String, table: String): List<DbTrigger> =
    dbGet(dbTableUrl(projectID, connID, schema, table, "triggers"))

/**
 * One page of a table. [filter] is the user-typed raw expression (design §6); [sort] "" = unsorted.
 * Uses the long-timeout client — the server still caps the call at 30 s (504).
 */
fun Api.dbRows(
    projectID: String, connID: String, schema: String, table: String,
    limit: Int, offset: Int, sort: String, sortDesc: Boolean, filter: String,
): DbRows {
    val u = dbTableUrl(projectID, connID, schema, table, "rows").newBuilder().apply {
        addQueryParameter("limit", limit.toString())
        addQueryParameter("offset", offset.toString())
        if (sort.isNotEmpty()) {
            addQueryParameter("sort", sort)
            if (sortDesc) addQueryParameter("sort_desc", "true")
        }
        if (filter.isNotBlank()) addQueryParameter("filter", filter)
    }.build()
    return Api.netClient.newCall(request("GET", u)).execute().use {
        check(it)
        Api.json.decodeFromString(it.jsonText())
    }
}

/**
 * Runs any SQL. Cancelling the calling coroutine cancels the HTTP call (D6); the server-side statement
 * still runs to its 30 s deadline. `has_more` means the result hit [limit] rows.
 */
suspend fun Api.dbQuery(projectID: String, connID: String, sql: String, limit: Int = 500): DbRows =
    suspendCancellableCoroutine { cont ->
        val u = dbUrl(projectID, connID, "query").newBuilder().addQueryParameter("limit", limit.toString()).build()
        val call = Api.netClient.newCall(request("POST", u, bodyOf("sql" to sql)))
        cont.invokeOnCancellation { call.cancel() }
        call.enqueue(object : Callback {
            override fun onFailure(call: Call, e: IOException) = cont.resumeWithException(e)
            override fun onResponse(call: Call, response: Response) {
                response.use {
                    try {
                        check(it)
                        cont.resume(Api.json.decodeFromString<DbRows>(it.jsonText()))
                    } catch (e: Exception) {
                        cont.resumeWithException(e)
                    }
                }
            }
        })
    }

/**
 * Applies [edits] in one server-side transaction. The runtime never reports rows affected, so a
 * non-matching update/delete still succeeds — callers verify by re-fetching (D3).
 */
fun Api.dbCommit(projectID: String, connID: String, schema: String, table: String, edits: List<DbEdit>) {
    val body = buildJsonObject {
        put("edits", buildJsonArray {
            edits.forEach { e ->
                add(buildJsonObject {
                    put("op", e.op)
                    put("original", JsonObject(e.original))
                    put("values", JsonObject(e.values))
                })
            }
        })
    }.toString().toRequestBody("application/json".toMediaType())
    Api.netClient.newCall(request("PATCH", dbTableUrl(projectID, connID, schema, table, "rows"), body)).execute().use { check(it) }
}

private fun jsonBody(obj: JsonObject) = obj.toString().toRequestBody("application/json".toMediaType())

private fun DbConnForm.fields(b: kotlinx.serialization.json.JsonObjectBuilder) = b.apply {
    put("name", name); put("engine", engine); put("host", host); put("port", port); put("username", username)
    put("database", database); put("ssl_mode", ssl_mode); put("instance_name", instance_name); put("search_path", search_path)
}

/**
 * Creates ([id] null) or updates a connection. Create sends [password] flat; update sends `secret`
 * only when [password] is non-blank, and omits `extra_params` so the web-set value is kept (D4).
 */
fun Api.dbSave(projectID: String, id: String?, f: DbConnForm, password: String): DbConnection {
    val req = if (id == null) {
        request("POST", dbUrl(projectID), jsonBody(buildJsonObject { f.fields(this); put("password", password) }))
    } else {
        request("PATCH", dbUrl(projectID, id), jsonBody(buildJsonObject {
            f.fields(this)
            if (password.isNotEmpty()) put("secret", buildJsonObject { put("password", password) })
        }))
    }
    return Api.client.newCall(req).execute().use {
        check(it)
        Api.json.decodeFromString(it.jsonText())
    }
}

fun Api.dbDelete(projectID: String, connID: String) {
    Api.client.newCall(request("DELETE", dbUrl(projectID, connID))).execute().use { check(it) }
}

// ---- principals & grants (not offered for SQLite: it has no server-side users) ----

fun Api.dbPrincipals(projectID: String, connID: String): List<DbPrincipal> = dbGet(dbUrl(projectID, connID, "principals"))

fun Api.dbCreatePrincipal(projectID: String, connID: String, name: String, password: String, canLogin: Boolean, superuser: Boolean) {
    val body = jsonBody(buildJsonObject { put("name", name); put("password", password); put("can_login", canLogin); put("superuser", superuser) })
    Api.client.newCall(request("POST", dbUrl(projectID, connID, "principals"), body)).execute().use { check(it) }
}

fun Api.dbDropPrincipal(projectID: String, connID: String, name: String) {
    Api.client.newCall(request("DELETE", dbUrl(projectID, connID, "principals", name))).execute().use { check(it) }
}

fun Api.dbSetPrincipalPassword(projectID: String, connID: String, name: String, password: String) {
    val body = jsonBody(buildJsonObject { put("password", password) })
    Api.client.newCall(request("PATCH", dbUrl(projectID, connID, "principals", name), body)).execute().use { check(it) }
}

fun Api.dbGrants(projectID: String, connID: String, name: String): List<DbGrant> =
    dbGet(dbUrl(projectID, connID, "principals", name, "grants"))

/** [revoke] false grants, true revokes (`…/grants/revoke`). */
fun Api.dbChangeGrant(projectID: String, connID: String, name: String, g: DbGrant, revoke: Boolean) {
    val url = if (revoke) dbUrl(projectID, connID, "principals", name, "grants", "revoke") else dbUrl(projectID, connID, "principals", name, "grants")
    val body = jsonBody(buildJsonObject { put("privilege", g.privilege); put("schema", g.schema); put("table", g.table) })
    Api.client.newCall(request("POST", url, body)).execute().use { check(it) }
}

// ---- backups (docs/DESIGN_ANDROID_DATABASES.md slice 8) ----

fun Api.dbBackups(projectID: String, connID: String): List<DbBackup> = dbGet(dbUrl(projectID, connID, "backups"))

/** Starts a dump; answers 202 with the `running` row. */
fun Api.dbCreateBackup(projectID: String, connID: String): DbBackup =
    Api.client.newCall(request("POST", dbUrl(projectID, connID, "backups"), bodyOf())).execute().use {
        check(it)
        Api.json.decodeFromString(it.jsonText())
    }

fun Api.dbDeleteBackup(projectID: String, connID: String, backupID: String) {
    Api.client.newCall(request("DELETE", dbUrl(projectID, connID, "backups", backupID))).execute().use { check(it) }
}

/** The connection's schedule, or null when none is set (the server answers 404 "no schedule"). */
fun Api.dbSchedule(projectID: String, connID: String): DbSchedule? =
    Api.client.newCall(request("GET", dbUrl(projectID, connID, "backups", "schedule"))).execute().use {
        if (it.code == 404) return null
        check(it)
        Api.json.decodeFromString(it.jsonText())
    }

fun Api.dbPutSchedule(projectID: String, connID: String, cadence: String, retention: Int) {
    val body = jsonBody(buildJsonObject { put("cadence", cadence); put("retention", retention) })
    Api.client.newCall(request("PUT", dbUrl(projectID, connID, "backups", "schedule"), body)).execute().use { check(it) }
}

fun Api.dbDeleteSchedule(projectID: String, connID: String) {
    Api.client.newCall(request("DELETE", dbUrl(projectID, connID, "backups", "schedule"))).execute().use { check(it) }
}

/** Streams the dump into [out] (the server relays it from the runtime in 4 MiB chunks). */
fun Api.dbDownloadBackup(projectID: String, connID: String, backupID: String, out: java.io.OutputStream) {
    Api.netClient.newCall(request("GET", dbUrl(projectID, connID, "backups", backupID, "download"))).execute().use {
        check(it)
        it.body?.byteStream()?.copyTo(out)
    }
}

// The server waits up to 30 minutes for a restore reply, so the phone must not give up at 150 s.
private val restoreClient by lazy {
    Api.netClient.newBuilder().readTimeout(31, java.util.concurrent.TimeUnit.MINUTES).build()
}

/** Restores into [target] (a database name; a file path for SQLite). Blocks until the runtime finishes. */
fun Api.dbRestore(projectID: String, connID: String, backupID: String, target: String) {
    val body = jsonBody(buildJsonObject { put("target_database", target); put("confirm", true) })
    restoreClient.newCall(request("POST", dbUrl(projectID, connID, "backups", backupID, "restore"), body)).execute().use { check(it) }
}

/** Tests the *saved* connection (no draft-test route exists). */
fun Api.dbTest(projectID: String, connID: String): DbTestResult =
    Api.netClient.newCall(request("POST", dbUrl(projectID, connID, "test"), bodyOf())).execute().use {
        check(it)
        Api.json.decodeFromString(it.jsonText())
    }
