package dev.devstationone.net

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement
import okhttp3.HttpUrl

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
