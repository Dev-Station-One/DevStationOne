package dev.devstationone.ui

import dev.devstationone.net.DbConnection
import dev.devstationone.net.DbConstraint
import dev.devstationone.net.DbIndex
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
