package dev.devstationone.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull

/**
 * Read-only result grid (design D2): a header row and a LazyColumn of rows,
 * one shared horizontal scroll state, first column pinned. Cells show at most
 * 200 chars on one line — the row sheet shows full values. Serves the data
 * browser now and SQL results later.
 */
@Composable
internal fun ResultGrid(
    columns: List<String>,
    rows: List<List<JsonElement>>,
    sort: DbSort,
    onSort: ((String) -> Unit)?,
    onRow: (Int) -> Unit,
    modifier: Modifier = Modifier,
) {
    if (columns.isEmpty()) return
    val scroll = rememberScrollState()
    val widths = columns.mapIndexed { i, c -> columnWidthDp(c, rows, i).dp }
    Column(modifier) {
        GridRow(
            columns.mapIndexed { i, c -> (c + if (sort.column == c) (if (sort.desc) " ▼" else " ▲") else "") to false },
            widths, scroll, Modifier.background(MaterialTheme.colorScheme.surfaceVariant),
            bold = true, onCell = onSort?.let { f -> { i -> f(columns[i]) } },
        )
        LazyColumn(Modifier.fillMaxSize()) {
            itemsIndexed(rows) { idx, r ->
                GridRow(
                    columns.indices.map { i -> (r.getOrNull(i) ?: JsonNull).let { cellText(it).take(200) to (it is JsonNull) } },
                    widths, scroll, Modifier.clickable { onRow(idx) },
                )
            }
        }
    }
}

@Composable
private fun GridRow(
    cells: List<Pair<String, Boolean>>,
    widths: List<androidx.compose.ui.unit.Dp>,
    scroll: androidx.compose.foundation.ScrollState,
    modifier: Modifier,
    bold: Boolean = false,
    onCell: ((Int) -> Unit)? = null,
) {
    @Composable
    fun Cell(i: Int) {
        val (text, isNull) = cells[i]
        Text(
            text, maxLines = 1, overflow = TextOverflow.Ellipsis, fontSize = 12.sp, fontFamily = FontFamily.Monospace,
            fontWeight = if (bold) FontWeight.Bold else null,
            fontStyle = if (isNull) FontStyle.Italic else null,
            color = if (isNull) MaterialTheme.colorScheme.onSurfaceVariant else Color.Unspecified,
            modifier = Modifier.width(widths[i]).then(if (onCell != null) Modifier.clickable { onCell(i) } else Modifier).padding(horizontal = 6.dp, vertical = 8.dp),
        )
    }
    Row(modifier) {
        Cell(0) // pinned first column
        Row(Modifier.horizontalScroll(scroll)) { for (i in 1 until cells.size) Cell(i) }
    }
}
