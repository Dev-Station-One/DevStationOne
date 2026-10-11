package dev.devstationone.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.background
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.ui.draw.clip
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import dev.devstationone.data.SqlHistoryStore
import dev.devstationone.net.Api
import dev.devstationone.net.DbRows
import dev.devstationone.net.dbColumns
import dev.devstationone.net.dbQuery
import dev.devstationone.net.dbSchemas
import dev.devstationone.net.dbTables
import dev.devstationone.ui.kit.MuxIcons
import dev.devstationone.ui.kit.ScreenHeader
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch

private const val SQL_ROW_LIMIT = 500

/**
 * SQL editor (design D7/D8, slice 4; round 4: sora editor with highlighting and schema completion): any statement runs; a non-read-only
 * one asks first, quoting it. Cancel aborts the HTTP call — the server-side statement still runs to
 * its 30 s deadline. History (20 per connection) is app-private. SQL text is never logged.
 */
@Composable
internal fun SqlView(api: Api, projectID: String, r: DbRoute.Sql, onBack: () -> Unit) {
    val scope = rememberCoroutineScope()
    val store = SqlHistoryStore(LocalContext.current)
    val editor = remember { SqlEditorHandle() }
    val loader = remember(r) {
        SqlSchemaLoader(
            r.conn.engine, scope,
            fetchTables = {
                val s = r.schema.ifEmpty { if (hasSchemas(r.conn.engine)) defaultSchema(r.conn.engine, api.dbSchemas(projectID, r.conn.id)) else "" }
                s to api.dbTables(projectID, r.conn.id, s)
            },
            fetchColumns = { s, t -> api.dbColumns(projectID, r.conn.id, s, t) },
        )
    }
    LaunchedEffect(loader) { loader.load() }
    var noticeDismissed by remember { mutableStateOf(false) }
    var history by remember { mutableStateOf(store.load(r.conn.id)) }
    var result by remember { mutableStateOf<DbRows?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var job by remember { mutableStateOf<Job?>(null) }
    var confirm by remember { mutableStateOf(false) }
    var historyOpen by remember { mutableStateOf(false) }
    var rowOpen by remember { mutableStateOf<Int?>(null) }

    fun exec() {
        val text = editor.text.trim()
        history = pushHistory(history, text).also { store.save(r.conn.id, it) }
        result = null
        error = null
        job = scope.launch {
            try {
                result = api.dbQuery(projectID, r.conn.id, text, SQL_ROW_LIMIT)
            } catch (e: CancellationException) {
                error = "Cancelled"
                throw e
            } catch (e: Exception) {
                error = dbErrorText(e.message)
            } finally {
                job = null
            }
        }
    }

    fun run() {
        val sql = editor.text
        if (sql.isBlank() || job != null) return
        if (isReadOnlySql(sql)) exec() else confirm = true
    }

    Column(Modifier.fillMaxSize()) {
        ScreenHeader("SQL · ${r.conn.name}", onBack = onBack)
        if (loader.failed.value && !noticeDismissed) {
            Row(Modifier.padding(horizontal = 16.dp), verticalAlignment = Alignment.CenterVertically) {
                Text("Couldn't load the schema — keyword completion only.", fontSize = 13.sp, modifier = Modifier.weight(1f))
                TextButton(onClick = { noticeDismissed = true }) { Text("Dismiss") }
            }
        }
        SqlEditor(editor, loader, Modifier.padding(horizontal = 16.dp))
        Row(Modifier.padding(horizontal = 8.dp), horizontalArrangement = Arrangement.spacedBy(4.dp)) {
            if (job == null) Box(
                Modifier.size(52.dp).alpha(if (editor.hasText.value) 1f else 0.4f).clip(RoundedCornerShape(10.dp))
                    .background(MaterialTheme.colorScheme.surfaceVariant)
                    .clickable(enabled = editor.hasText.value, onClickLabel = "Run") { run() },
                contentAlignment = Alignment.Center,
            ) { Icon(MuxIcons.Play, "Run", Modifier.size(36.dp), tint = MaterialTheme.colorScheme.primary) }
            else TextButton(onClick = { job?.cancel() }) { Text("Cancel") }
            TextButton(enabled = history.isNotEmpty(), onClick = { historyOpen = true }) { Text("History ▾") }
            DropdownMenu(historyOpen, onDismissRequest = { historyOpen = false }) {
                history.forEach { h ->
                    DropdownMenuItem(
                        text = { Text(h.lines().first(), maxLines = 1, overflow = TextOverflow.Ellipsis, fontFamily = FontFamily.Monospace, fontSize = 12.sp) },
                        onClick = { editor.setText(h); historyOpen = false },
                    )
                }
            }
        }
        if (job != null) Text("Running…", fontSize = 13.sp, modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp))
        error?.let { Text(it, color = MaterialTheme.colorScheme.error, fontSize = 13.sp, modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp)) }
        result?.let { res ->
            when {
                res.is_exec -> Text("${res.rows_affected} rows affected", modifier = Modifier.padding(16.dp))
                else -> {
                    Text(
                        if (res.has_more) "First $SQL_ROW_LIMIT rows — truncated" else pageLabel(0, res.rows.size),
                        fontSize = 13.sp, modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp),
                    )
                    ResultGrid(res.columns, res.rows, DbSort(), onSort = null, onRow = { rowOpen = it }, modifier = Modifier.weight(1f))
                }
            }
        }
    }

    if (confirm) {
        AlertDialog(
            onDismissRequest = { confirm = false },
            title = { Text("Run this statement?") },
            text = {
                Column {
                    Text("It may modify data on ${r.conn.name}.")
                    Text(editor.text.trim().take(300), fontFamily = FontFamily.Monospace, fontSize = 12.sp, modifier = Modifier.padding(top = 8.dp))
                }
            },
            confirmButton = { TextButton(onClick = { confirm = false; exec() }) { Text("Run") } },
            dismissButton = { TextButton(onClick = { confirm = false }) { Text("Cancel") } },
        )
    }
    rowOpen?.let { i -> result?.let { RowSheet(it.columns, it.rows[i], onDismiss = { rowOpen = null }) } }
}
