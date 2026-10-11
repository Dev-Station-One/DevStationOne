package dev.devstationone.ui

import android.os.Bundle
import androidx.compose.runtime.mutableStateOf
import dev.devstationone.net.DbColumn
import dev.devstationone.net.DbTable
import io.github.rosemoe.sora.lang.completion.CompletionItemKind
import io.github.rosemoe.sora.lang.completion.CompletionPublisher
import io.github.rosemoe.sora.lang.completion.SimpleCompletionItem
import io.github.rosemoe.sora.langs.textmate.TextMateLanguage
import io.github.rosemoe.sora.langs.textmate.registry.GrammarRegistry
import io.github.rosemoe.sora.langs.textmate.registry.ThemeRegistry
import io.github.rosemoe.sora.text.CharPosition
import io.github.rosemoe.sora.text.ContentReference
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit
import kotlinx.coroutines.withContext
import org.eclipse.tm4e.core.grammar.IGrammar
import org.eclipse.tm4e.languageconfiguration.internal.model.LanguageConfiguration
import java.util.concurrent.ConcurrentHashMap

// SQL completion for the Databases SQL editor (docs/DESIGN_ANDROID_ROUND4.md D9-D12). The pure parts
// (completionAt, aliasMap, suggest, insertText) are unit-tested in DbCompletionTest; SqlSchemaLoader and
// SqlLanguage are the sora/coroutine glue. Table and column names stay in memory and are never logged.

/** Tables whose columns load up front; the rest load when their name is typed (D10). */
internal const val SCHEMA_PRELOAD = 50
private const val SCHEMA_CONCURRENCY = 4
internal const val MAX_SUGGESTIONS = 50

private val ANSI_KEYWORDS = listOf(
    "SELECT", "FROM", "WHERE", "GROUP", "BY", "ORDER", "HAVING", "LIMIT", "OFFSET", "JOIN", "INNER", "LEFT",
    "RIGHT", "FULL", "OUTER", "CROSS", "ON", "USING", "AS", "AND", "OR", "NOT", "NULL", "IS", "IN", "EXISTS",
    "BETWEEN", "LIKE", "DISTINCT", "UNION", "ALL", "INTERSECT", "EXCEPT", "CASE", "WHEN", "THEN", "ELSE", "END",
    "ASC", "DESC", "INSERT", "INTO", "VALUES", "UPDATE", "SET", "DELETE", "CREATE", "TABLE", "VIEW", "INDEX",
    "DROP", "ALTER", "ADD", "COLUMN", "PRIMARY", "KEY", "FOREIGN", "REFERENCES", "UNIQUE", "DEFAULT", "CHECK",
    "WITH", "EXPLAIN", "TRUE", "FALSE", "COUNT", "SUM", "AVG", "MIN", "MAX", "COALESCE", "CAST", "UPPER", "LOWER",
    "LENGTH", "SUBSTRING", "TRIM", "ROUND", "ABS", "NOW", "OVER", "PARTITION", "ROW_NUMBER", "BEGIN", "COMMIT",
    "ROLLBACK", "TRUNCATE",
)

private val ENGINE_KEYWORDS = mapOf(
    "postgresql" to listOf("ILIKE", "RETURNING", "SERIAL", "BIGSERIAL", "JSONB", "ARRAY"),
    "mssql" to listOf("TOP", "NOLOCK", "IDENTITY", "NVARCHAR", "GETDATE", "ISNULL"),
    "sqlite" to listOf("PRAGMA", "AUTOINCREMENT", "GLOB", "IFNULL"),
    "mysql" to listOf("SHOW", "DESCRIBE", "AUTO_INCREMENT", "IFNULL", "DATABASES", "TABLES"),
    "mariadb" to listOf("SHOW", "DESCRIBE", "AUTO_INCREMENT", "IFNULL", "DATABASES", "TABLES"),
)

private val ALL_KEYWORDS: Set<String> = (ANSI_KEYWORDS + ENGINE_KEYWORDS.values.flatten()).toSet()

internal fun keywordsFor(engine: String): List<String> = (ANSI_KEYWORDS + ENGINE_KEYWORDS[engine].orEmpty()).distinct().sorted()

/** What the user is typing: the identifier [prefix] before the cursor, an optional `qualifier.` and whether an opening quote precedes it. */
internal class CompletionCtx(val prefix: String, val qualifier: String?, val quoted: Boolean)

private fun isIdent(c: Char) = c.isLetterOrDigit() || c == '_' || c == '$'

/** Null = no popup (empty prefix and nothing before it to complete against). */
internal fun completionAt(text: CharSequence, cursor: Int): CompletionCtx? {
    var start = cursor
    while (start > 0 && isIdent(text[start - 1])) start--
    val prefix = text.substring(start, cursor)
    var qualifier: String? = null
    if (start > 0 && text[start - 1] == '.') {
        var end = start - 1
        if (end > 0 && text[end - 1] in "\"`]") end--
        var q = end
        while (q > 0 && isIdent(text[q - 1])) q--
        qualifier = text.substring(q, end).ifEmpty { null }
    }
    val quoted = qualifier == null && start > 0 && text[start - 1] in "\"`["
    if (prefix.isEmpty() && qualifier == null && !quoted) return null
    return CompletionCtx(prefix, qualifier, quoted)
}

private val ALIAS_RE = Regex("""\b(?:from|join)\s+([\w."`\[\]]+)(?:\s+(?:as\s+)?(\w+))?""", RegexOption.IGNORE_CASE)

/**
 * `FROM orders o` / `JOIN public.orders AS o` → `o → orders` (lower-cased alias). Simple aliases only:
 * subqueries and CTEs are not resolved, which just means "all columns" is offered instead.
 */
internal fun aliasMap(text: CharSequence): Map<String, String> {
    val out = HashMap<String, String>()
    for (m in ALIAS_RE.findAll(text)) {
        val table = m.groupValues[1].substringAfterLast('.').trim('"', '`', '[', ']')
        val alias = m.groupValues[2]
        if (alias.isNotEmpty() && table.isNotEmpty() && alias.uppercase() !in ALL_KEYWORDS) out[alias.lowercase()] = table
    }
    return out
}

/** Immutable snapshot read by sora's completion thread (D10). */
internal class SqlSchema(val tables: List<DbTable> = emptyList(), val columns: Map<String, List<DbColumn>> = emptyMap()) {
    fun table(name: String): DbTable? = tables.firstOrNull { it.name.equals(name, ignoreCase = true) }
    fun columnsOf(name: String): List<DbColumn> = columns.entries.firstOrNull { it.key.equals(name, ignoreCase = true) }?.value.orEmpty()
}

internal enum class SuggestKind { KEYWORD, TABLE, COLUMN }

internal data class Suggestion(val text: String, val kind: SuggestKind, val detail: String = "")

/**
 * Keywords, then tables, then columns, each alphabetical, capped. After `x.` only the columns of table/alias
 * `x`; when `x` is no table (a schema, say) the matching tables are offered instead.
 */
internal fun suggest(ctx: CompletionCtx, schema: SqlSchema, engine: String, aliases: Map<String, String>): List<Suggestion> {
    val p = ctx.prefix.lowercase()
    fun String.hit() = lowercase().startsWith(p)
    val tables = schema.tables.filter { it.name.hit() }.sortedBy { it.name.lowercase() }.map { Suggestion(it.name, SuggestKind.TABLE, it.kind) }
    val q = ctx.qualifier
    if (q != null) {
        val target = aliases[q.lowercase()] ?: q
        if (schema.table(target) == null) return tables.take(MAX_SUGGESTIONS)
        return schema.columnsOf(target).filter { it.name.hit() }.sortedBy { it.name.lowercase() }
            .map { Suggestion(it.name, SuggestKind.COLUMN, it.type) }.take(MAX_SUGGESTIONS)
    }
    val keywords = keywordsFor(engine).filter { it.hit() }.map { Suggestion(it, SuggestKind.KEYWORD) }
    val columns = schema.columns.entries.flatMap { (t, cols) -> cols.filter { it.name.hit() }.map { Suggestion(it.name, SuggestKind.COLUMN, t) } }
        .sortedWith(compareBy({ it.text.lowercase() }, { it.detail.lowercase() }))
    return (keywords + tables + columns).take(MAX_SUGGESTIONS)
}

private val PLAIN_IDENT = Regex("[a-z_][a-z0-9_]*")

/** The text inserted for [name]: bare when it is a plain lower-case identifier, otherwise quoted for [engine]. */
internal fun insertText(engine: String, name: String): String = when {
    PLAIN_IDENT.matches(name) && name.uppercase() !in ALL_KEYWORDS -> name
    engine == "mysql" || engine == "mariadb" -> "`" + name.replace("`", "``") + "`"
    engine == "mssql" -> "[" + name.replace("]", "]]") + "]"
    else -> "\"" + name.replace("\"", "\"\"") + "\""
}

/**
 * Loads the connection's table list and then the columns of the first [SCHEMA_PRELOAD] tables, at most
 * [SCHEMA_CONCURRENCY] calls at a time; further tables load once on demand via [want]. A failed table list
 * sets [failed] (keywords-only completion); a failed column call just skips that table.
 */
internal class SqlSchemaLoader(
    val engine: String,
    private val scope: CoroutineScope,
    private val fetchTables: () -> Pair<String, List<DbTable>>,
    private val fetchColumns: (schema: String, table: String) -> List<DbColumn>,
) {
    @Volatile var schema = SqlSchema()
        private set
    val failed = mutableStateOf(false)
    private var schemaName = ""
    private val requested = ConcurrentHashMap.newKeySet<String>()

    @Synchronized private fun publish(f: (SqlSchema) -> SqlSchema) { schema = f(schema) }

    suspend fun load() = coroutineScope {
        val (name, tables) = try {
            withContext(Dispatchers.IO) { fetchTables() }
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            failed.value = true
            return@coroutineScope
        }
        schemaName = name
        publish { SqlSchema(tables, it.columns) }
        val gate = Semaphore(SCHEMA_CONCURRENCY)
        tables.take(SCHEMA_PRELOAD).forEach { t -> launch { gate.withPermit { ensureColumns(t.name) } } }
    }

    private suspend fun ensureColumns(table: String) {
        if (!requested.add(table.lowercase())) return
        try {
            val cols = withContext(Dispatchers.IO) { fetchColumns(schemaName, table) }
            publish { SqlSchema(it.tables, it.columns + (table to cols)) }
        } catch (e: CancellationException) {
            requested.remove(table.lowercase())
            throw e
        } catch (_: Exception) {
            // that table simply has no column completion
        }
    }

    /** Called from the completion thread: fetch [name]'s columns once if it is a known table whose columns are not loaded. */
    fun want(name: String) {
        val t = schema.table(name)?.name ?: return
        if (t.lowercase() in requested) return
        scope.launch { ensureColumns(t) }
    }
}

/** The vendored SQL TextMate language plus the schema-aware completion popup. */
internal class SqlLanguage private constructor(
    grammar: IGrammar,
    config: LanguageConfiguration?,
    private val loader: SqlSchemaLoader,
) : TextMateLanguage(grammar, config, GrammarRegistry.getInstance(), ThemeRegistry.getInstance(), false) {

    override fun requireAutoComplete(content: ContentReference, position: CharPosition, publisher: CompletionPublisher, extraArguments: Bundle) {
        val text = StringBuilder()
        var cursor = 0
        for (i in 0 until content.lineCount) {
            if (i == position.line) cursor = text.length + position.column
            content.appendLineTo(text, i)
            text.append('\n')
        }
        val ctx = completionAt(text, cursor) ?: return
        val aliases = aliasMap(text)
        ctx.qualifier?.let { loader.want(aliases[it.lowercase()] ?: it) }
        loader.want(ctx.prefix)
        val prefixLen = ctx.prefix.length + if (ctx.quoted) 1 else 0
        suggest(ctx, loader.schema, loader.engine, aliases).forEach { s ->
            val kind = when (s.kind) {
                SuggestKind.KEYWORD -> CompletionItemKind.Keyword
                SuggestKind.TABLE -> CompletionItemKind.Struct
                SuggestKind.COLUMN -> CompletionItemKind.Field
            }
            val commit = if (s.kind == SuggestKind.KEYWORD) s.text else insertText(loader.engine, s.text)
            publisher.addItem(SimpleCompletionItem(s.text, s.detail, prefixLen, commit).kind(kind))
        }
    }

    companion object {
        fun create(loader: SqlSchemaLoader): SqlLanguage {
            val reg = GrammarRegistry.getInstance()
            val grammar = reg.findGrammar("source.sql") ?: error("SQL grammar missing from assets/textmate")
            return SqlLanguage(grammar, reg.findLanguageConfiguration("source.sql"), loader)
        }
    }
}
