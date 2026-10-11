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
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshots.SnapshotStateList
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import dev.devstationone.data.Server
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
import dev.devstationone.net.dbSchemas
import dev.devstationone.net.dbTables
import dev.devstationone.net.dbTriggers
import dev.devstationone.ui.kit.MuxCard
import dev.devstationone.ui.kit.MuxChip
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
    val stack: SnapshotStateList<DbRoute> = remember(projectID) { mutableStateListOf(DbRoute.Connections) }
    BackHandler(enabled = active && stack.size > 1) { stack.removeLast() }

    when (val r = stack.last()) {
        DbRoute.Connections -> ConnectionsView(api, projectID, onOpen = { stack += DbRoute.Tables(it) })
        is DbRoute.Tables -> TablesView(
            api, projectID, r.conn,
            onBack = { stack.removeLast() },
            onOpen = { schema, table -> stack += DbRoute.Data(r.conn, schema, table) },
            onSql = { stack += DbRoute.Sql(r.conn) },
        )
        is DbRoute.Sql -> SqlView(api, projectID, r, onBack = { stack.removeLast() })
        is DbRoute.Data -> DataView(
            api, projectID, r,
            onBack = { stack.removeLast() },
            onStructure = { stack += DbRoute.Structure(r.conn, r.schema, r.table) },
        )
        is DbRoute.Structure -> StructureView(api, projectID, r, onBack = { stack.removeLast() })
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
private fun ConnectionsView(api: Api, projectID: String, onOpen: (DbConnection) -> Unit) {
    Column(Modifier.fillMaxSize()) {
        ScreenHeader("Databases")
        Loaded(projectID, { api.dbConnections(projectID) }) { conns ->
            if (conns.isEmpty()) Empty("No database connections yet. Add one from the web UI.")
            LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                items(conns, key = { it.id }) { c ->
                    MuxCard(onClick = { onOpen(c) }) {
                        Column(Modifier.padding(12.dp)) {
                            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                Text(c.name, style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
                                Text(engineLabel(c.engine), fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
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
private fun TablesView(api: Api, projectID: String, conn: DbConnection, onBack: () -> Unit, onOpen: (schema: String, table: String) -> Unit, onSql: () -> Unit) {
    Column(Modifier.fillMaxSize()) {
        ScreenHeader(conn.name, onBack = onBack, actions = { TextButton(onClick = onSql) { Text("SQL") } })
        if (hasSchemas(conn.engine)) {
            Loaded(conn.id, { api.dbSchemas(projectID, conn.id) }) { schemas ->
                var schema by remember(schemas) { mutableStateOf(defaultSchema(conn.engine, schemas)) }
                SchemaPicker(schema, schemas) { schema = it }
                TableList(api, projectID, conn, schema, onOpen)
            }
        } else {
            TableList(api, projectID, conn, "", onOpen)
        }
    }
}

@Composable
private fun SchemaPicker(schema: String, schemas: List<String>, onPick: (String) -> Unit) {
    var open by remember { mutableStateOf(false) }
    TextButton(onClick = { open = true }, modifier = Modifier.padding(horizontal = 8.dp)) { Text("Schema: $schema ▾") }
    DropdownMenu(open, onDismissRequest = { open = false }) {
        schemas.forEach { s -> DropdownMenuItem(text = { Text(s) }, onClick = { open = false; onPick(s) }) }
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

private val STRUCTURE_SEGMENTS = listOf("Columns", "Indexes", "Constraints", "Triggers")

@Composable
private fun StructureView(api: Api, projectID: String, r: DbRoute.Structure, onBack: () -> Unit) {
    var segment by remember { mutableIntStateOf(0) }
    Column(Modifier.fillMaxSize()) {
        ScreenHeader(r.table, onBack = onBack)
        Row(Modifier.padding(horizontal = 16.dp, vertical = 4.dp), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            STRUCTURE_SEGMENTS.forEachIndexed { i, label -> MuxChip(label, selected = segment == i, onClick = { segment = i }) }
        }
        Loaded(r, {
            Structure(
                api.dbColumns(projectID, r.conn.id, r.schema, r.table),
                api.dbIndexes(projectID, r.conn.id, r.schema, r.table),
                api.dbConstraints(projectID, r.conn.id, r.schema, r.table),
                api.dbTriggers(projectID, r.conn.id, r.schema, r.table),
            )
        }) { s ->
            val pk = primaryKeyColumns(s.constraints, s.indexes)
            LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(horizontal = 16.dp, vertical = 4.dp)) {
                when (segment) {
                    0 -> items(s.columns, key = { it.name }) { c ->
                        StructureRow(
                            (if (c.name in pk) "🔑 " else "") + c.name,
                            listOfNotNull(c.type, if (c.nullable) null else "not null", c.default.ifEmpty { null }?.let { "default $it" }).joinToString(" · "),
                        )
                    }
                    1 -> items(s.indexes, key = { it.name }) { i ->
                        StructureRow(i.name, listOfNotNull(i.columns.joinToString(", "), if (i.primary) "primary" else if (i.unique) "unique" else null).joinToString(" · "))
                    }
                    2 -> items(s.constraints, key = { it.name }) { c ->
                        StructureRow(c.name, listOfNotNull(c.type, c.columns.joinToString(", "), c.references.ifEmpty { null }?.let { "→ $it" }).joinToString(" · "))
                    }
                    else -> items(s.triggers, key = { it.name }) { t ->
                        StructureRow(t.name, listOf(t.timing, t.event).filter { it.isNotEmpty() }.joinToString(" "))
                    }
                }
                val count = when (segment) { 0 -> s.columns.size; 1 -> s.indexes.size; 2 -> s.constraints.size; else -> s.triggers.size }
                if (count == 0) item { Empty("None.") }
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
