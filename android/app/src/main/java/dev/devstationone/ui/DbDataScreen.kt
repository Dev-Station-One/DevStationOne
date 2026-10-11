package dev.devstationone.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import dev.devstationone.net.Api
import dev.devstationone.net.DbColumn
import dev.devstationone.net.DbEdit
import dev.devstationone.net.DbRows
import dev.devstationone.net.dbColumns
import dev.devstationone.net.dbCommit
import dev.devstationone.net.dbConstraints
import dev.devstationone.net.dbIndexes
import dev.devstationone.net.dbRows
import dev.devstationone.ui.kit.ScreenHeader
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.JsonElement

/** Column types + primary key of the browsed table: what row identity and form fields derive from. */
private class TableMeta(val columns: List<DbColumn>, val pk: List<String>)

/** [page] null = insert; otherwise the row at [idx] of that page. */
private class FormTarget(val page: DbRows?, val idx: Int)

/**
 * Data browser (design §5, slices 3+5): raw filter, header-tap sort, has_more paging, row sheet with
 * Edit/Delete, and Insert. Row identity is the primary key only; an update/delete is verified by
 * re-fetching the page because the runtime never reports rows affected (D3). Filter text and row
 * values are user data and never logged.
 */
@Composable
internal fun DataView(api: Api, projectID: String, r: DbRoute.Data, onBack: () -> Unit, onStructure: () -> Unit) {
    val scope = rememberCoroutineScope()
    var filterDraft by remember { mutableStateOf("") }
    var filter by remember { mutableStateOf("") }
    var sort by remember { mutableStateOf(DbSort()) }
    var offset by remember { mutableIntStateOf(0) }
    var tick by remember { mutableIntStateOf(0) }
    var meta by remember(r) { mutableStateOf<TableMeta?>(null) }
    var sheet by remember { mutableStateOf<Pair<DbRows, Int>?>(null) }
    var form by remember { mutableStateOf<FormTarget?>(null) }
    var deleting by remember { mutableStateOf<Pair<DbRows, Int>?>(null) }
    var busy by remember { mutableStateOf(false) }
    var formError by remember { mutableStateOf<String?>(null) }
    var notice by remember { mutableStateOf<String?>(null) }

    LaunchedEffect(r) {
        meta = try {
            withContext(Dispatchers.IO) {
                TableMeta(
                    api.dbColumns(projectID, r.conn.id, r.schema, r.table),
                    primaryKeyColumns(api.dbConstraints(projectID, r.conn.id, r.schema, r.table), api.dbIndexes(projectID, r.conn.id, r.schema, r.table)),
                )
            }
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            null
        }
    }

    fun apply() { filter = filterDraft.trim(); offset = 0 }

    fun fetchPage(): DbRows =
        api.dbRows(projectID, r.conn.id, r.schema, r.table, DB_PAGE_SIZE, offset, sort.column, sort.desc, filter)

    /** Commits one edit; update/delete re-fetch the page and verify it landed (D3). */
    fun commit(op: String, target: FormTarget, values: Map<String, JsonElement>) {
        val m = meta ?: return
        busy = true
        formError = null
        scope.launch {
            try {
                val page = target.page
                val old = page?.rows?.get(target.idx)
                val edit = DbEdit(op, if (page != null && old != null) pkOriginal(page.columns, m.pk, old) else emptyMap(), values)
                val landed = withContext(Dispatchers.IO) {
                    api.dbCommit(projectID, r.conn.id, r.schema, r.table, listOf(edit))
                    // ponytail: grid reloads once more via tick; one extra page fetch per edit.
                    if (page == null || old == null) true else editLanded(op, page.columns, m.pk, old, fetchPage().rows)
                }
                notice = if (landed) "Saved" else "No row matched — nothing changed"
                form = null; deleting = null; sheet = null
                tick++
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                formError = dbErrorText(e.message)
            }
            busy = false
        }
    }

    fun submit(target: FormTarget, fields: Map<String, FieldState>) {
        val m = meta ?: return
        val row = target.page?.let { p -> p.columns.zip(p.rows[target.idx]).toMap() }
        val res = buildValues(m.columns, fields, row)
        when {
            res.error != null -> formError = res.error
            res.values.isEmpty() -> formError = if (target.page == null) "Enter a value for at least one column" else "No changes"
            else -> commit(if (target.page == null) "insert" else "update", target, res.values)
        }
    }

    Column(Modifier.fillMaxSize()) {
        ScreenHeader(r.table, onBack = onBack, actions = {
            TextButton(enabled = meta != null, onClick = { formError = null; form = FormTarget(null, 0) }) { Text("+ Row") }
            TextButton(onClick = onStructure) { Text("Structure") }
        })
        OutlinedTextField(
            value = filterDraft, onValueChange = { filterDraft = it },
            modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp),
            singleLine = true,
            placeholder = { Text("Filter, e.g. id > 10") },
            trailingIcon = { TextButton(onClick = { apply() }) { Text("Apply") } },
            keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
            keyboardActions = KeyboardActions(onSearch = { apply() }),
        )
        notice?.let { Text(it, fontSize = 13.sp, color = MaterialTheme.colorScheme.primary, modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp)) }
        Loaded(listOf(r, filter, sort, offset, tick), { fetchPage() }) { page ->
            Row(Modifier.fillMaxWidth().padding(horizontal = 8.dp), horizontalArrangement = Arrangement.SpaceBetween) {
                TextButton(enabled = offset > 0, onClick = { offset = maxOf(0, offset - DB_PAGE_SIZE) }) { Text("‹ Prev") }
                Text(pageLabel(offset, page.rows.size), fontSize = 13.sp, modifier = Modifier.padding(top = 12.dp))
                TextButton(enabled = page.has_more, onClick = { offset += DB_PAGE_SIZE }) { Text("Next ›") }
            }
            if (page.rows.isEmpty()) Text("No rows.", color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(16.dp))
            ResultGrid(
                page.columns, page.rows, sort,
                onSort = { c -> sort = nextSort(sort, c); offset = 0 },
                onRow = { sheet = page to it },
            )
        }
    }

    sheet?.let { (page, idx) ->
        val m = meta
        val blocked = if (m == null) "Table structure unavailable — editing is off." else editBlockedReason(m.pk, m.columns)
        RowSheet(page.columns, page.rows[idx], onDismiss = { sheet = null }, actions = {
            if (blocked != null) {
                Text(blocked, fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
            } else {
                Row {
                    TextButton(onClick = { formError = null; form = FormTarget(page, idx) }) { Text("Edit") }
                    TextButton(onClick = { formError = null; deleting = page to idx }) { Text("Delete", color = MaterialTheme.colorScheme.error) }
                }
            }
        })
    }

    form?.let { t ->
        val m = meta
        if (m != null) {
            RowForm(
                title = if (t.page == null) "Insert row" else "Edit row",
                columns = m.columns,
                row = t.page?.let { p -> p.columns.zip(p.rows[t.idx]).toMap() },
                error = formError, busy = busy,
                onSubmit = { submit(t, it) },
                onDismiss = { form = null; formError = null },
            )
        }
    }

    deleting?.let { (page, idx) ->
        val m = meta
        if (m != null) {
            val key = pkOriginal(page.columns, m.pk, page.rows[idx]).entries.joinToString(", ") { "${it.key} = ${cellText(it.value)}" }
            AlertDialog(
                onDismissRequest = { if (!busy) deleting = null },
                title = { Text("Delete row?") },
                text = {
                    Column {
                        Text("Deletes the row where", fontSize = 14.sp)
                        Text(key, fontFamily = FontFamily.Monospace, fontSize = 13.sp, modifier = Modifier.padding(top = 4.dp))
                        Text("from ${r.table}. This can't be undone.", fontSize = 14.sp, modifier = Modifier.padding(top = 4.dp))
                        formError?.let { Text(it, color = MaterialTheme.colorScheme.error, fontSize = 13.sp, modifier = Modifier.padding(top = 8.dp)) }
                    }
                },
                confirmButton = {
                    TextButton(enabled = !busy, onClick = { commit("delete", FormTarget(page, idx), emptyMap()) }) {
                        Text(if (busy) "Deleting…" else "Delete", color = MaterialTheme.colorScheme.error)
                    }
                },
                dismissButton = { TextButton(enabled = !busy, onClick = { deleting = null }) { Text("Cancel") } },
            )
        }
    }
}
