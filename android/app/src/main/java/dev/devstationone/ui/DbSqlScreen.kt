package dev.devstationone.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import dev.devstationone.data.SqlHistoryStore
import dev.devstationone.net.Api
import dev.devstationone.net.DbRows
import dev.devstationone.net.dbQuery
import dev.devstationone.ui.kit.ScreenHeader
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch

private const val SQL_ROW_LIMIT = 500

/**
 * SQL editor (design D7/D8, slice 4): plain monospace field, any statement runs; a non-read-only
 * one asks first, quoting it. Cancel aborts the HTTP call — the server-side statement still runs to
 * its 30 s deadline. History (20 per connection) is app-private. SQL text is never logged.
 */
@Composable
internal fun SqlView(api: Api, projectID: String, r: DbRoute.Sql, onBack: () -> Unit) {
    val scope = rememberCoroutineScope()
    val store = SqlHistoryStore(LocalContext.current)
    var sql by remember { mutableStateOf("") }
    var history by remember { mutableStateOf(store.load(r.conn.id)) }
    var result by remember { mutableStateOf<DbRows?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var job by remember { mutableStateOf<Job?>(null) }
    var confirm by remember { mutableStateOf(false) }
    var historyOpen by remember { mutableStateOf(false) }
    var rowOpen by remember { mutableStateOf<Int?>(null) }

    fun exec() {
        val text = sql.trim()
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
        if (sql.isBlank() || job != null) return
        if (isReadOnlySql(sql)) exec() else confirm = true
    }

    Column(Modifier.fillMaxSize()) {
        ScreenHeader("SQL · ${r.conn.name}", onBack = onBack)
        OutlinedTextField(
            value = sql, onValueChange = { sql = it },
            modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp),
            minLines = 4, maxLines = 8,
            textStyle = TextStyle(fontFamily = FontFamily.Monospace, fontSize = 13.sp),
            placeholder = { Text("SELECT …") },
        )
        Row(Modifier.padding(horizontal = 8.dp), horizontalArrangement = Arrangement.spacedBy(4.dp)) {
            if (job == null) TextButton(enabled = sql.isNotBlank(), onClick = { run() }) { Text("Run") }
            else TextButton(onClick = { job?.cancel() }) { Text("Cancel") }
            TextButton(enabled = history.isNotEmpty(), onClick = { historyOpen = true }) { Text("History ▾") }
            DropdownMenu(historyOpen, onDismissRequest = { historyOpen = false }) {
                history.forEach { h ->
                    DropdownMenuItem(
                        text = { Text(h.lines().first(), maxLines = 1, overflow = TextOverflow.Ellipsis, fontFamily = FontFamily.Monospace, fontSize = 12.sp) },
                        onClick = { sql = h; historyOpen = false },
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
                    Text(sql.trim().take(300), fontFamily = FontFamily.Monospace, fontSize = 12.sp, modifier = Modifier.padding(top = 8.dp))
                }
            },
            confirmButton = { TextButton(onClick = { confirm = false; exec() }) { Text("Run") } },
            dismissButton = { TextButton(onClick = { confirm = false }) { Text("Cancel") } },
        )
    }
    rowOpen?.let { i -> result?.let { RowSheet(it.columns, it.rows[i]) { rowOpen = null } } }
}
