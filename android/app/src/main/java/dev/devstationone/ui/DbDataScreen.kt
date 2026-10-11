package dev.devstationone.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import dev.devstationone.net.Api
import dev.devstationone.net.DbRows
import dev.devstationone.net.dbRows
import dev.devstationone.ui.kit.ScreenHeader

/**
 * Read-only data browser (design §5, slice 3): raw filter expression, header
 * tap sort, has_more paging, and a bottom sheet with every column of a tapped
 * row. Editing is slice 5. Filter text is user-typed SQL and never logged.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun DataView(api: Api, projectID: String, r: DbRoute.Data, onBack: () -> Unit, onStructure: () -> Unit) {
    var filterDraft by remember { mutableStateOf("") }
    var filter by remember { mutableStateOf("") }
    var sort by remember { mutableStateOf(DbSort()) }
    var offset by remember { mutableIntStateOf(0) }
    var open by remember { mutableStateOf<Pair<DbRows, Int>?>(null) }

    fun apply() { filter = filterDraft.trim(); offset = 0 }

    Column(Modifier.fillMaxSize()) {
        ScreenHeader(r.table, onBack = onBack, actions = { TextButton(onClick = onStructure) { Text("Structure") } })
        OutlinedTextField(
            value = filterDraft, onValueChange = { filterDraft = it },
            modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp),
            singleLine = true,
            placeholder = { Text("Filter, e.g. id > 10") },
            trailingIcon = { TextButton(onClick = { apply() }) { Text("Apply") } },
            keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
            keyboardActions = KeyboardActions(onSearch = { apply() }),
        )
        Loaded(listOf(r, filter, sort, offset), {
            api.dbRows(projectID, r.conn.id, r.schema, r.table, DB_PAGE_SIZE, offset, sort.column, sort.desc, filter)
        }) { page ->
            Row(Modifier.fillMaxWidth().padding(horizontal = 8.dp), horizontalArrangement = Arrangement.SpaceBetween) {
                TextButton(enabled = offset > 0, onClick = { offset = maxOf(0, offset - DB_PAGE_SIZE) }) { Text("‹ Prev") }
                Text(pageLabel(offset, page.rows.size), fontSize = 13.sp, modifier = Modifier.padding(top = 12.dp))
                TextButton(enabled = page.has_more, onClick = { offset += DB_PAGE_SIZE }) { Text("Next ›") }
            }
            if (page.rows.isEmpty()) Text("No rows.", color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(16.dp))
            ResultGrid(
                page.columns, page.rows, sort,
                onSort = { c -> sort = nextSort(sort, c); offset = 0 },
                onRow = { open = page to it },
            )
        }
    }

    open?.let { (page, idx) ->
        ModalBottomSheet(onDismissRequest = { open = null }) {
            LazyColumn(Modifier.padding(horizontal = 16.dp)) {
                itemsIndexed(page.columns) { i, col ->
                    Column(Modifier.fillMaxWidth().padding(vertical = 6.dp)) {
                        Text(col, fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        Text(page.rows[idx].getOrNull(i)?.let(::cellText) ?: "NULL", fontFamily = FontFamily.Monospace, fontSize = 14.sp)
                    }
                }
            }
        }
    }
}
