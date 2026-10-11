package dev.devstationone.ui

import dev.devstationone.data.DbRef
import dev.devstationone.net.DbBackup
import dev.devstationone.net.DbColumn
import dev.devstationone.net.DbConnection
import dev.devstationone.net.DbConstraint
import dev.devstationone.net.DbGrant
import dev.devstationone.net.DbIndex
import dev.devstationone.ui.kit.DropdownOption
import kotlinx.coroutines.CancellationException
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

// Pure helpers for the Databases tab (docs/DESIGN_ANDROID_DATABASES.md D3/§5); unit-tested in DbLogicTest.

/** Tab-local back stack entries (D1). */
sealed interface DbRoute {
    data object Connections : DbRoute
    data class Tables(val conn: DbConnection) : DbRoute
    /** [schema] = the schema browsed when SQL was opened; "" for SQLite or when unknown. */
    data class Sql(val conn: DbConnection, val schema: String = "") : DbRoute
    data class Backups(val conn: DbConnection) : DbRoute
    data class Principals(val conn: DbConnection) : DbRoute
    data class Grants(val conn: DbConnection, val principal: String) : DbRoute
    /** [conn] null = new connection. */
    data class Form(val conn: DbConnection?) : DbRoute
    data class Data(val conn: DbConnection, val schema: String, val table: String) : DbRoute
    data class Structure(val conn: DbConnection, val schema: String, val table: String) : DbRoute
}

/** SQLite has no schemas; the picker is hidden and `schema` is never sent. */
internal fun hasSchemas(engine: String): Boolean = engine != "sqlite"

/** Initial schema: "" for SQLite, postgres prefers `public`, otherwise the first one the server lists. */
internal fun defaultSchema(engine: String, schemas: List<String>): String = when {
    !hasSchemas(engine) -> ""
    engine == "postgresql" && "public" in schemas -> "public"
    else -> schemas.firstOrNull().orEmpty()
}

/** D3: primary-key columns come from the primary_key constraint, falling back to a primary index. */
internal fun primaryKeyColumns(constraints: List<DbConstraint>, indexes: List<DbIndex>): List<String> =
    constraints.firstOrNull { it.type == "primary_key" }?.columns
        ?: indexes.firstOrNull { it.primary }?.columns
        ?: emptyList()

/** The server answers `{"error":"..."}`; show that text, else the raw message, capped at 200 chars (design §6). */
internal fun dbErrorText(message: String?): String {
    val raw = message.orEmpty().trim()
    val text = try {
        Json.parseToJsonElement(raw).jsonObject["error"]?.jsonPrimitive?.contentOrNull ?: raw
    } catch (_: Exception) {
        raw
    }
    return text.take(200)
}

/** Subtitle on a connection card: `host:port/db`, or just the file path for SQLite. */
internal fun connectionTarget(c: DbConnection): String =
    if (c.engine == "sqlite") c.database
    else buildString {
        append(c.host)
        if (c.port > 0) append(':').append(c.port)
        if (c.database.isNotEmpty()) append('/').append(c.database)
    }

internal fun engineLabel(engine: String): String = when (engine) {
    "postgresql" -> "PostgreSQL"
    "mysql" -> "MySQL"
    "mssql" -> "SQL Server"
    "sqlite" -> "SQLite"
    else -> engine
}

/** Engine dropdown entries, MySQL first; MariaDB joins in round 5. */
internal val ENGINE_OPTIONS = listOf("mysql", "postgresql", "mssql", "sqlite").map { DropdownOption(it, engineLabel(it)) }

// ---- Databases route restore (round 4, D3) ----

/** The restorable part of [stack] in order; the Connections root and the (secret-bearing) Form are never stored. */
internal fun refsOf(stack: List<DbRoute>): List<DbRef> = stack.mapNotNull {
    when (it) {
        DbRoute.Connections, is DbRoute.Form -> null
        is DbRoute.Tables -> DbRef("tables", it.conn.id)
        is DbRoute.Sql -> DbRef("sql", it.conn.id, schema = it.schema)
        is DbRoute.Backups -> DbRef("backups", it.conn.id)
        is DbRoute.Principals -> DbRef("users", it.conn.id)
        is DbRoute.Grants -> DbRef("grants", it.conn.id, user = it.principal)
        is DbRoute.Data -> DbRef("data", it.conn.id, it.schema, it.table)
        is DbRoute.Structure -> DbRef("structure", it.conn.id, it.schema, it.table)
    }
}

/** [routes] reopened in order; [failed] is the first ref that no longer resolves (null = all did). */
internal class Restored(val routes: List<DbRoute>, val failed: DbRef?)

/** Resolves [refs] in order and stops at the first failure so the last valid ancestor stays on top. */
internal suspend fun restoreStack(refs: List<DbRef>, resolve: suspend (DbRef) -> DbRoute?): Restored {
    val routes = mutableListOf<DbRoute>()
    for (ref in refs) {
        val route = try { resolve(ref) } catch (e: CancellationException) { throw e } catch (_: Exception) { null }
        if (route == null) return Restored(routes, ref)
        routes += route
    }
    return Restored(routes, null)
}

internal const val DB_PAGE_SIZE = 100

/** Column sort state; [column] "" = unsorted. */
internal data class DbSort(val column: String = "", val desc: Boolean = false)

/** Header tap cycles asc → desc → none on one column; a different column starts at asc (design §5). */
internal fun nextSort(cur: DbSort, column: String): DbSort = when {
    cur.column != column -> DbSort(column, false)
    !cur.desc -> DbSort(column, true)
    else -> DbSort()
}

/** "Rows 101–200" for a page of [count] rows at [offset]; "No rows" when empty (no totals exist, only has_more). */
internal fun pageLabel(offset: Int, count: Int): String =
    if (count == 0) "No rows" else "Rows ${offset + 1}–${offset + count}"

/** Grid/sheet text for one JSON cell: null → NULL, strings unquoted, everything else as its JSON text. */
internal fun cellText(e: JsonElement): String = when {
    e is JsonNull -> "NULL"
    e is JsonPrimitive && e.isString -> e.content
    else -> e.toString()
}

/** Grid column width in dp: header and first 20 sampled cells × 7 dp, clamped 72..200 (D2). */
internal fun columnWidthDp(header: String, rows: List<List<JsonElement>>, col: Int): Int {
    val longest = maxOf(header.length, rows.take(20).maxOfOrNull { r -> r.getOrNull(col)?.let { cellText(it).length } ?: 0 } ?: 0)
    return (longest * 7).coerceIn(72, 200)
}

private val READ_ONLY_FIRST_WORDS = setOf("SELECT", "WITH", "EXPLAIN", "SHOW", "PRAGMA", "DESCRIBE", "VALUES", "TABLE")

/**
 * D7, UX only — never a security control. Comments are stripped (quote-aware), then the statement is
 * read-only iff its first keyword is in the read set and no `;` remains before the end. A `;` inside
 * a string literal counts as a second statement (false positive, the safe direction). Known false
 * negative: `WITH … DELETE`.
 */
internal fun isReadOnlySql(sql: String): Boolean {
    val code = StringBuilder()
    var quote = ' '
    var i = 0
    while (i < sql.length) {
        val c = sql[i]
        when {
            quote != ' ' -> { code.append(c); if (c == quote) quote = ' ' }
            c == '\'' || c == '"' || c == '`' -> { quote = c; code.append(c) }
            sql.startsWith("--", i) -> { i = sql.indexOf('\n', i).let { if (it < 0) sql.length else it }; continue }
            sql.startsWith("/*", i) -> { i = sql.indexOf("*/", i + 2).let { if (it < 0) sql.length else it + 2 }; code.append(' '); continue }
            else -> code.append(c)
        }
        i++
    }
    val body = code.toString().trim().trimEnd { it == ';' || it.isWhitespace() }
    return ';' !in body && body.takeWhile { it.isLetter() }.uppercase() in READ_ONLY_FIRST_WORDS
}

internal const val SQL_HISTORY_MAX = 20

/** Newest-first, de-duplicated, capped history after running [sql]. */
internal fun pushHistory(history: List<String>, sql: String): List<String> =
    (listOf(sql.trim()) + history.filter { it != sql.trim() }).take(SQL_HISTORY_MAX)

// ---- backups ----

/** Polling continues only while some backup is still being written. */
internal fun anyRunning(list: List<DbBackup>): Boolean = list.any { it.state == "running" }

/** Restore needs the target name typed back exactly (design §5; the server only checks `confirm`). */
internal fun restoreConfirmed(typed: String, target: String): Boolean = target.isNotBlank() && typed.trim() == target.trim()

/** Retention is 1–100 on the server; the cadence comes from fixed chips. */
internal fun parseRetention(text: String): Int? = text.trim().toIntOrNull()?.takeIf { it in 1..100 }

/** `2026-10-11T04:05:06Z` → `2026-10-11 04:05` (UTC; the server sends ISO text). */
internal fun whenText(iso: String): String = iso.take(16).replace('T', ' ')

// ---- principals & grants ----

/** SQLite has no server-side users or roles — the screen says so instead of calling the API. */
internal fun userManagementSupported(engine: String): Boolean = engine != "sqlite"

internal fun validatePrincipal(name: String, password: String): String? = when {
    name.isBlank() -> "Name is required"
    password.isEmpty() -> "Password is required"
    else -> null
}

internal fun validateGrant(privilege: String, table: String): String? = when {
    privilege.isBlank() -> "Privilege is required"
    table.isBlank() -> "Table is required"
    else -> null
}

/** `schema.table` (or just table); structured grants only — raw grants have no label. */
internal fun grantTarget(g: DbGrant): String = if (g.schema.isEmpty()) g.table else "${g.schema}.${g.table}"

// ---- connection form (design §5) ----

internal fun defaultPort(engine: String): Int = when (engine) {
    "postgresql" -> 5432
    "mysql" -> 3306
    "mssql" -> 1433
    else -> 0
}

/** Mirrors the server's validateConnectionFields plus a port range check; null = ok. */
internal fun validateConnection(engine: String, name: String, host: String, port: String, database: String): String? = when {
    name.isBlank() -> "Name is required"
    engine != "sqlite" && host.isBlank() -> "Host is required"
    engine != "sqlite" && port.isNotBlank() && port.trim().toIntOrNull()?.let { it in 1..65535 } != true -> "Port must be 1–65535"
    database.isBlank() -> if (engine == "sqlite") "File path is required" else "Database is required"
    else -> null
}

// ---- row editing (design D3/D5) ----

private val INT_TYPES = setOf(
    "integer", "int", "int2", "int4", "int8", "bigint", "smallint", "tinyint", "mediumint",
    "serial", "bigserial", "smallserial",
)
private val FLOAT_TYPES = setOf("float", "float4", "float8", "real", "double", "double precision")
private val NUMERIC_TYPES = setOf("numeric", "decimal") + FLOAT_TYPES
private val BOOL_TYPES = setOf("boolean", "bool", "bit")

private fun baseType(type: String): String = type.lowercase().substringBefore('(').trim().removeSuffix(" unsigned")

/** Why Edit/Delete are off for this table, or null when a safe row identity exists (D3). */
internal fun editBlockedReason(pk: List<String>, columns: List<DbColumn>): String? = when {
    pk.isEmpty() -> "No primary key — rows can't be matched safely, so Edit and Delete are off."
    pk.any { k -> columns.firstOrNull { it.name == k }?.let { baseType(it.type) in FLOAT_TYPES } == true } ->
        "Floating-point key — rows can't be matched reliably, so Edit and Delete are off."
    else -> null
}

/** D5: text → JSON by column type; null when [text] isn't valid for it. Everything non-numeric/bool stays a string. */
internal fun coerceValue(type: String, text: String): JsonElement? = when (baseType(type)) {
    in INT_TYPES -> text.trim().toLongOrNull()?.let { JsonPrimitive(it) }
    in NUMERIC_TYPES -> text.trim().let { s ->
        s.toLongOrNull()?.let { JsonPrimitive(it) } ?: s.toDoubleOrNull()?.takeIf { it.isFinite() }?.let { JsonPrimitive(it) }
    }
    in BOOL_TYPES -> when (text.trim().lowercase()) {
        "true", "t", "1" -> JsonPrimitive(true)
        "false", "f", "0" -> JsonPrimitive(false)
        else -> null
    }
    else -> JsonPrimitive(text)
}

/** Per-column edit input: KEEP = untouched (omitted on update; DEFAULT on insert), VALUE = typed text, NULL. */
internal enum class FieldMode { KEEP, VALUE, NULL }

internal data class FieldState(val mode: FieldMode = FieldMode.KEEP, val text: String = "")

internal class EditResult(val values: Map<String, JsonElement>, val error: String?)

/**
 * Turns form state into the commit `values` map. [row] = the fetched row for an update (only changed
 * columns are sent), null for an insert (every touched column is sent).
 */
internal fun buildValues(columns: List<DbColumn>, fields: Map<String, FieldState>, row: Map<String, JsonElement>?): EditResult {
    val out = linkedMapOf<String, JsonElement>()
    for (c in columns) {
        val f = fields[c.name] ?: continue
        val current = row?.get(c.name)
        when (f.mode) {
            FieldMode.KEEP -> {}
            FieldMode.NULL -> {
                if (!c.nullable) return EditResult(emptyMap(), "${c.name} is not nullable")
                if (current !is JsonNull) out[c.name] = JsonNull
            }
            FieldMode.VALUE -> {
                val v = coerceValue(c.type, f.text) ?: return EditResult(emptyMap(), "${c.name}: not a valid ${c.type}")
                if (current == null || current is JsonNull || cellText(current) != f.text) out[c.name] = v
            }
        }
    }
    return EditResult(out, null)
}

/** The row's primary-key cells exactly as fetched — the only `original` ever sent (D3). */
internal fun pkOriginal(columns: List<String>, pk: List<String>, row: List<JsonElement>): Map<String, JsonElement> =
    pk.associateWith { row[columns.indexOf(it)] }

/**
 * D3 verification after an update/delete: the runtime never reports rows affected, so look at the
 * re-fetched page. A delete landed iff the PK is gone; an update landed iff the PK is gone from
 * this page (sorted/filtered away) or the row differs. Same PK + identical row ⇒ nothing matched.
 */
internal fun editLanded(op: String, columns: List<String>, pk: List<String>, old: List<JsonElement>, fresh: List<List<JsonElement>>): Boolean {
    val idx = pk.map { columns.indexOf(it) }
    val match = fresh.firstOrNull { r -> idx.all { r.getOrNull(it) == old[it] } }
    return when (op) {
        "delete" -> match == null
        else -> match == null || match != old
    }
}
