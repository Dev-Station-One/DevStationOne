package dev.devstationone.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import dev.devstationone.net.DbColumn
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull

/**
 * Edit (row != null) or insert (row == null) form, one field per column (design D5). Update: fields
 * start prefilled and only changed ones are sent. Insert: untouched fields are omitted so the
 * database default applies; NULL is explicit. Values are never logged.
 */
@Composable
internal fun RowForm(
    title: String,
    columns: List<DbColumn>,
    row: Map<String, JsonElement>?,
    error: String?,
    busy: Boolean,
    onSubmit: (Map<String, FieldState>) -> Unit,
    onDismiss: () -> Unit,
) {
    var fields by remember { mutableStateOf(emptyMap<String, FieldState>()) }
    AlertDialog(
        onDismissRequest = { if (!busy) onDismiss() },
        title = { Text(title) },
        text = {
            Column(Modifier.heightIn(max = 460.dp).verticalScroll(rememberScrollState())) {
                columns.forEach { c ->
                    val cell = row?.get(c.name)
                    val f = fields[c.name] ?: FieldState()
                    val shown = when (f.mode) {
                        FieldMode.VALUE -> f.text
                        FieldMode.NULL -> ""
                        FieldMode.KEEP -> if (cell == null || cell is JsonNull) "" else cellText(cell)
                    }
                    Row(Modifier.fillMaxWidth().padding(top = 8.dp)) {
                        OutlinedTextField(
                            value = shown,
                            onValueChange = { fields = fields + (c.name to FieldState(FieldMode.VALUE, it)) },
                            modifier = Modifier.weight(1f),
                            label = { Text("${c.name} · ${c.type}${if (c.nullable) "" else " · not null"}", fontSize = 12.sp) },
                            placeholder = {
                                Text(
                                    when {
                                        f.mode == FieldMode.NULL || cell is JsonNull -> "NULL"
                                        row == null && c.default.isNotEmpty() -> "default: ${c.default}"
                                        row == null -> "default"
                                        else -> ""
                                    },
                                )
                            },
                            textStyle = androidx.compose.ui.text.TextStyle(fontFamily = FontFamily.Monospace, fontSize = 13.sp),
                        )
                        if (c.nullable) {
                            TextButton(onClick = {
                                fields = fields + (c.name to if (f.mode == FieldMode.NULL) FieldState() else FieldState(FieldMode.NULL))
                            }) { Text(if (f.mode == FieldMode.NULL) "undo" else "NULL") }
                        }
                    }
                }
                error?.let { Text(it, color = MaterialTheme.colorScheme.error, fontSize = 13.sp, modifier = Modifier.padding(top = 8.dp)) }
            }
        },
        confirmButton = { TextButton(enabled = !busy, onClick = { onSubmit(fields) }) { Text(if (busy) "Saving…" else "Save") } },
        dismissButton = { TextButton(enabled = !busy, onClick = onDismiss) { Text("Cancel") } },
    )
}
