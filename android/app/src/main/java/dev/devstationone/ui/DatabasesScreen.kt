package dev.devstationone.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.foundation.layout.Box
import androidx.compose.ui.Alignment
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshots.SnapshotStateList
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import dev.devstationone.data.DbRef
import dev.devstationone.data.Server
import dev.devstationone.data.UiStateStore
import dev.devstationone.net.Api
import dev.devstationone.net.DbColumn
import dev.devstationone.net.DbConnection
import dev.devstationone.net.DbConstraint
import dev.devstationone.net.DbIndex
import dev.devstationone.net.DbTrigger
import dev.devstationone.net.dbColumns
import dev.devstationone.net.dbConnections
import dev.devstationone.net.dbConstraints
import dev.devstationone.net.dbIndexes
import dev.devstationone.net.dbPrincipals
import dev.devstationone.net.dbSchemas
import dev.devstationone.net.dbTables
import dev.devstationone.net.dbTriggers
import dev.devstationone.ui.kit.MuxCard
import dev.devstationone.ui.kit.DropdownOption
import dev.devstationone.ui.kit.MuxDropdown
import dev.devstationone.ui.kit.ScreenHeader
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/**
 * The Databases tab (docs/DESIGN_ANDROID_DATABASES.md D1): a private back
 * stack Connections → Tables → Structure. Back pops it while this tab is
 * selected and deeper than the root; at the root it falls through to the
 * workspace's own back handler.
 */
@Composable
fun DatabasesScreen(server: Server, projectID: String, active: Boolean) {
    val api = remember(server) { Api(server) }
    val ui = UiStateStore(LocalContext.current)
    val stack: SnapshotStateList<DbRoute> = remember(projectID) { mutableStateListOf(DbRoute.Connections) }
    var rev by remember { mutableIntStateOf(0) } // bumped after a save/delete so the list reloads
    val saved = remember(server.id, projectID) { ui.db(server.id, projectID) }
    // Persisting waits for the restore so the initial lone Connections root cannot overwrite what is being reopened.
    var persisting by remember(server.id, projectID) { mutableStateOf(saved.isEmpty()) }
    var notice by remember { mutableStateOf<String?>(null) }
    BackHandler(enabled = active && stack.size > 1) { stack.removeLast() }

    // Restore once, when the tab is first shown (D3): validate each saved route with one cheap call, stop at the first failure.
    LaunchedEffect(active, persisting) {
        if (!active || persisting) return@LaunchedEffect
        val restored = try {
            withContext(Dispatchers.IO) {
                val conns = api.dbConnections(projectID)
                restoreStack(saved) { ref -> resolveRef(api, projectID, conns, ref) }
            }
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            Restored(emptyList(), saved.firstOrNull())
        }
        // Any navigation while validating wins; never jump the user to another screen.
        if (stack.size == 1 && stack[0] == DbRoute.Connections) stack.addAll(restored.routes)
        restored.failed?.let { notice = "Couldn't reopen ${it.table.ifEmpty { it.user }.ifEmpty { "the last screen" }}" }
        persisting = true
    }
    LaunchedEffect(stack.toList(), persisting) {
        if (persisting) ui.setDb(server.id, projectID, refsOf(stack))
    }

    Column(Modifier.fillMaxSize()) {
        notice?.let { n ->
            Row(Modifier.padding(horizontal = 16.dp), verticalAlignment = Alignment.CenterVertically) {
                Text(n, fontSize = 13.sp, modifier = Modifier.weight(1f))
                TextButton(onClick = { notice = null }) { Text("Dismiss") }
            }
        }
        Box(Modifier.weight(1f).fillMaxSize()) {
            when (val r = stack.last()) {
                DbRoute.Connections -> ConnectionsView(
                    api, projectID, rev,
                    onOpen = { stack += DbRoute.Tables(it) },
                    onEdit = { stack += DbRoute.Form(it) },
                )
                is DbRoute.Form -> ConnectionForm(api, projectID, r, onBack = { stack.removeLast() }, onChanged = { rev++; stack.removeLast() })
                is DbRoute.Tables -> TablesView(
                    api, projectID, r.conn,
                    onBack = { stack.removeLast() },
                    onOpen = { schema, table -> stack += DbRoute.Data(r.conn, schema, table) },
                    onSql = { schema -> stack += DbRoute.Sql(r.conn, schema) },
                    onUsers = { stack += DbRoute.Principals(r.conn) },
                    onBackups = { stack += DbRoute.Backups(r.conn) },
                )
                is DbRoute.Backups -> BackupsView(api, projectID, r.conn, onBack = { stack.removeLast() })
                is DbRoute.Principals -> PrincipalsView(api, projectID, r.conn, onBack = { stack.removeLast() }, onGrants = { stack += DbRoute.Grants(r.conn, it) })
                is DbRoute.Grants -> GrantsView(api, projectID, r, onBack = { stack.removeLast() })
                is DbRoute.Sql -> SqlView(api, projectID, r, onBack = { stack.removeLast() })
                is DbRoute.Data -> DataView(
                    api, projectID, r,
                    onBack = { stack.removeLast() },
                    onStructure = { stack += DbRoute.Structure(r.conn, r.schema, r.table) },
                )
                is DbRoute.Structure -> StructureView(api, projectID, r, onBack = { stack.removeLast() })
            }
        }
    }
}

/** One cheap validation call per saved route (D3); null = no longer valid. Blocking, runs on IO. */
private fun resolveRef(api: Api, projectID: String, conns: List<DbConnection>, ref: DbRef): DbRoute? {
    val conn = conns.firstOrNull { it.id == ref.conn } ?: return null
    return when (ref.kind) {
        "tables" -> DbRoute.Tables(conn)
        "sql" -> DbRoute.Sql(conn, ref.schema)
        "users" -> DbRoute.Principals(conn)
        "backups" -> DbRoute.Backups(conn)
        "data", "structure" -> {
            if (api.dbTables(projectID, conn.id, ref.schema).none { it.name == ref.table }) null
            else if (ref.kind == "data") DbRoute.Data(conn, ref.schema, ref.table) else DbRoute.Structure(conn, ref.schema, ref.table)
        }
        "grants" -> if (api.dbPrincipals(projectID, conn.id).any { it.name == ref.user }) DbRoute.Grants(conn, ref.user) else null
        else -> null
    }
}

/**
 * Fetches [fetch] off the main thread whenever [key] changes or Retry is hit.
 * Failures show the server message verbatim (offline runtime, 504, 400 …).
 */
@Composable
internal fun <T> Loaded(key: Any?, fetch: () -> T, content: @Composable (T) -> Unit) {
    class Box(val v: T)
    var data by remember(key) { mutableStateOf<Box?>(null) }
    var error by remember(key) { mutableStateOf<String?>(null) }
    var tick by remember(key) { mutableIntStateOf(0) }
    LaunchedEffect(key, tick) {
        try {
            data = Box(withContext(Dispatchers.IO) { fetch() })
            error = null
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            error = dbErrorText(e.message)
        }
    }
    error?.let { e ->
        Column(Modifier.fillMaxWidth().padding(16.dp)) {
            Text(e, color = MaterialTheme.colorScheme.error, fontSize = 13.sp)
            TextButton(onClick = { tick++ }) { Text("Retry") }
        }
    }
    val d = data
    when {
        d != null -> content(d.v)
        error == null -> CircularProgressIndicator(Modifier.padding(24.dp))
    }
}

@Composable
private fun Empty(text: String) =
    Text(text, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(16.dp))

@Composable
private fun ConnectionsView(api: Api, projectID: String, rev: Int, onOpen: (DbConnection) -> Unit, onEdit: (DbConnection?) -> Unit) {
    Column(Modifier.fillMaxSize()) {
        ScreenHeader("Databases", actions = { TextButton(onClick = { onEdit(null) }) { Text("+ Add") } })
        Loaded(projectID to rev, { api.dbConnections(projectID) }) { conns ->
            if (conns.isEmpty()) Empty("No database connections yet. Tap + Add.")
            LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                items(conns, key = { it.id }) { c ->
                    MuxCard(onClick = { onOpen(c) }) {
                        Column(Modifier.padding(12.dp)) {
                            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                Text(c.name, style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
                                Text(engineLabel(c.engine), fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                                Text("Edit", fontSize = 12.sp, color = MaterialTheme.colorScheme.primary, modifier = Modifier.clickable { onEdit(c) })
                            }
                            Text(connectionTarget(c), fontSize = 13.sp, fontFamily = FontFamily.Monospace, maxLines = 1, overflow = TextOverflow.Ellipsis, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun TablesView(api: Api, projectID: String, conn: DbConnection, onBack: () -> Unit, onOpen: (schema: String, table: String) -> Unit, onSql: (schema: String) -> Unit, onUsers: () -> Unit, onBackups: () -> Unit) {
    // Hoisted so the SQL button hands the browsed schema to the editor's completion (D11); "" until the schema list loads.
    var picked by remember(conn.id) { mutableStateOf<String?>(null) }
    var browsed by remember(conn.id) { mutableStateOf("") }
    Column(Modifier.fillMaxSize()) {
        ScreenHeader(conn.name, onBack = onBack, actions = { TextButton(onClick = { onSql(browsed) }) { Text("SQL") } })
        Row(Modifier.padding(horizontal = 8.dp)) {
            TextButton(onClick = onUsers) { Text("Users") }
            TextButton(onClick = onBackups) { Text("Backups") }
        }
        if (hasSchemas(conn.engine)) {
            Loaded(conn.id, { api.dbSchemas(projectID, conn.id) }) { schemas ->
                val schema = picked ?: defaultSchema(conn.engine, schemas)
                SideEffect { browsed = schema }
                MuxDropdown("Schema", schema, schemas.map { DropdownOption(it, it) }, { picked = it }, Modifier.padding(horizontal = 8.dp))
                TableList(api, projectID, conn, schema, onOpen)
            }
        } else {
            TableList(api, projectID, conn, "", onOpen)
        }
    }
}

@Composable
private fun TableList(api: Api, projectID: String, conn: DbConnection, schema: String, onOpen: (String, String) -> Unit) {
    Loaded(conn.id to schema, { api.dbTables(projectID, conn.id, schema) }) { tables ->
        if (tables.isEmpty()) Empty("No tables.")
        LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(horizontal = 16.dp, vertical = 4.dp)) {
            items(tables, key = { it.name }) { t ->
                Row(
                    Modifier.fillMaxWidth().clickable { onOpen(schema, t.name) }.padding(vertical = 10.dp),
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    Text(t.name, fontSize = 15.sp, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
                    if (t.kind != "table") Text(t.kind, fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
        }
    }
}

private class Structure(
    val columns: List<DbColumn>,
    val indexes: List<DbIndex>,
    val constraints: List<DbConstraint>,
    val triggers: List<DbTrigger>,
)

private enum class StructureKind(val label: String) { Columns("Columns"), Indexes("Indexes"), Constraints("Constraints"), Triggers("Triggers") }

@Composable
private fun StructureView(api: Api, projectID: String, r: DbRoute.Structure, onBack: () -> Unit) {
    var kind by remember { mutableStateOf(StructureKind.Columns) }
    Column(Modifier.fillMaxSize()) {
        ScreenHeader(r.table, onBack = onBack)
        Loaded(r, {
            Structure(
                api.dbColumns(projectID, r.conn.id, r.schema, r.table),
                api.dbIndexes(projectID, r.conn.id, r.schema, r.table),
                api.dbConstraints(projectID, r.conn.id, r.schema, r.table),
                api.dbTriggers(projectID, r.conn.id, r.schema, r.table),
            )
        }) { s ->
            val pk = primaryKeyColumns(s.constraints, s.indexes)
            val counts = mapOf(
                StructureKind.Columns to s.columns.size, StructureKind.Indexes to s.indexes.size,
                StructureKind.Constraints to s.constraints.size, StructureKind.Triggers to s.triggers.size,
            )
            Column(Modifier.fillMaxSize()) {
            MuxDropdown(
                "Structure", kind, StructureKind.entries.map { DropdownOption(it, it.label, counts.getValue(it).toString()) },
                { kind = it }, Modifier.padding(horizontal = 8.dp),
            )
            LazyColumn(Modifier.weight(1f).fillMaxWidth(), contentPadding = PaddingValues(horizontal = 16.dp, vertical = 4.dp)) {
                when (kind) {
                    StructureKind.Columns -> items(s.columns, key = { it.name }) { c ->
                        StructureRow(
                            (if (c.name in pk) "🔑 " else "") + c.name,
                            listOfNotNull(c.type, if (c.nullable) null else "not null", c.default.ifEmpty { null }?.let { "default $it" }).joinToString(" · "),
                        )
                    }
                    StructureKind.Indexes -> items(s.indexes, key = { it.name }) { i ->
                        StructureRow(i.name, listOfNotNull(i.columns.joinToString(", "), if (i.primary) "primary" else if (i.unique) "unique" else null).joinToString(" · "))
                    }
                    StructureKind.Constraints -> items(s.constraints, key = { it.name }) { c ->
                        StructureRow(c.name, listOfNotNull(c.type, c.columns.joinToString(", "), c.references.ifEmpty { null }?.let { "→ $it" }).joinToString(" · "))
                    }
                    StructureKind.Triggers -> items(s.triggers, key = { it.name }) { t ->
                        StructureRow(t.name, listOf(t.timing, t.event).filter { it.isNotEmpty() }.joinToString(" "))
                    }
                }
                if (counts.getValue(kind) == 0) item { Empty("None.") }
            }
            }
        }
    }
}

@Composable
private fun StructureRow(title: String, detail: String) {
    Column(Modifier.fillMaxWidth().padding(vertical = 8.dp)) {
        Text(title, fontSize = 15.sp, fontFamily = FontFamily.Monospace)
        if (detail.isNotEmpty()) Text(detail, fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}
