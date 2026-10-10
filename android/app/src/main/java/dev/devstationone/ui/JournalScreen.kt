package dev.devstationone.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
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
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import dev.devstationone.data.Server
import dev.devstationone.net.Api
import dev.devstationone.net.JournalNote
import dev.devstationone.net.JournalNoteSummary
import dev.devstationone.ui.kit.MuxCard
import dev.devstationone.ui.kit.MuxIcons
import dev.devstationone.ui.kit.RoundIconButton
import dev.devstationone.ui.kit.ScreenHeader
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

private val WHEN = DateTimeFormatter.ofPattern("MMM d, HH:mm").withZone(ZoneId.systemDefault())

/** ISO timestamp shown compactly; falls back to the date part when unparseable. */
private fun fmt(iso: String): String = try {
    WHEN.format(Instant.parse(iso))
} catch (_: Exception) {
    iso.take(10)
}

private fun fmtSize(n: Long): String = when {
    n >= 1_048_576 -> "%.1f MB".format(n / 1_048_576.0)
    n >= 1024 -> "%.0f KB".format(n / 1024.0)
    else -> "$n B"
}

/**
 * The project Journal tab: a paged list of the project's notes. Rows carry
 * the server-provided snippet (HTML stripped server-side), the update time,
 * the attachment count and the linked commit when present.
 */
@Composable
fun JournalScreen(server: Server, projectID: String, onOpen: (JournalNoteSummary) -> Unit) {
    val scope = rememberCoroutineScope()
    val api = remember { Api(server) }
    var notes by remember { mutableStateOf<List<JournalNoteSummary>>(emptyList()) }
    var total by remember { mutableIntStateOf(0) }
    var error by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }

    fun loadMore() {
        if (busy) return
        busy = true
        val offset = notes.size
        scope.launch {
            try {
                val p = withContext(Dispatchers.IO) { api.journal(projectID, 25, offset) }
                total = p.total
                notes = notes + p.notes.filter { seen -> notes.none { it.id == seen.id } }
                error = null
            } catch (e: Exception) {
                error = "Journal failed to load: ${e.message?.take(200)}"
            }
            busy = false
        }
    }
    LaunchedEffect(projectID) { loadMore() }

    Column(Modifier.fillMaxSize()) {
        Text(
            "Journal · ${if (total > 0) total else ""}",
            style = MaterialTheme.typography.labelLarge,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(horizontal = 16.dp, vertical = 10.dp),
        )
        error?.let {
            Text(it, color = MaterialTheme.colorScheme.error, fontSize = 12.sp, modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp))
        }
        LazyColumn(Modifier.weight(1f), contentPadding = PaddingValues(horizontal = 16.dp, vertical = 4.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            if (notes.isEmpty() && busy) {
                item { CircularProgressIndicator(Modifier.padding(top = 24.dp)) }
            }
            items(notes, key = { it.id }) { n ->
                MuxCard(onClick = { onOpen(n) }) {
                    Column(Modifier.padding(16.dp)) {
                        Text(
                            n.snippet.ifBlank { "(empty note)" },
                            style = MaterialTheme.typography.bodyMedium,
                            maxLines = 3,
                            overflow = TextOverflow.Ellipsis,
                        )
                        Row(Modifier.padding(top = 8.dp), verticalAlignment = Alignment.CenterVertically) {
                            Text(
                                fmt(n.updated_at),
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                            if (n.attachments.isNotEmpty()) {
                                Text("  📎 ${n.attachments.size}", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.primary)
                            }
                            if (n.commit_sha.isNotBlank()) {
                                Text(
                                    "  ${n.commit_sha.take(7)}",
                                    style = MaterialTheme.typography.bodySmall,
                                    modifier = Modifier.clip(RoundedCornerShape(6.dp)).background(MaterialTheme.colorScheme.surfaceVariant).padding(horizontal = 6.dp, vertical = 1.dp),
                                )
                            }
                        }
                    }
                }
            }
            if (notes.size < total) {
                item {
                    TextButton(enabled = !busy, onClick = { loadMore() }, modifier = Modifier.fillMaxWidth()) {
                        Text(if (busy) "Loading…" else "Load more (${notes.size} of $total)")
                    }
                }
            }
            if (notes.isEmpty() && !busy && error == null) {
                item { Text("No notes yet.", color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(top = 8.dp)) }
            }
        }
    }
}

/**
 * One journal note: attachment chips, the rendered (DOMPurify-sanitized)
 * body, and a menu with delete. Editing lands in the next stage.
 */
@Composable
fun NoteScreen(server: Server, projectID: String, projectName: String, noteID: String, onBack: () -> Unit) {
    val scope = rememberCoroutineScope()
    val api = remember { Api(server) }
    var note by remember { mutableStateOf<JournalNote?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var menu by remember { mutableStateOf(false) }
    var deleting by remember { mutableStateOf(false) }

    LaunchedEffect(noteID) {
        error = null
        try {
            note = withContext(Dispatchers.IO) { api.note(projectID, noteID) }
        } catch (e: Exception) {
            error = "Note failed to load: ${e.message?.take(200)}"
        }
    }
    BackHandler(onBack = onBack)

    Scaffold { pad ->
        Column(Modifier.padding(pad).fillMaxSize()) {
            ScreenHeader(
                title = projectName,
                onBack = onBack,
                actions = {
                    Box {
                        RoundIconButton(MuxIcons.More, "Menu") { menu = true }
                        DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                            DropdownMenuItem(text = { Text("Delete note…") }, onClick = { menu = false; deleting = true })
                        }
                    }
                },
            )
            val n = note
            when {
                error != null -> Text(error.orEmpty(), color = MaterialTheme.colorScheme.error, fontSize = 12.sp, modifier = Modifier.padding(16.dp))
                n == null -> CircularProgressIndicator(Modifier.padding(24.dp))
                else -> {
                    val meta = buildString {
                        append(fmt(n.created_at))
                        append(if (n.creator_name.isBlank()) "" else " · ${n.creator_name}")
                        append(" · updated ${fmt(n.updated_at)}")
                        if (n.commit_sha.isNotBlank()) append(" · ${n.commit_sha.take(7)} ${n.commit_subject}")
                    }
                    Text(meta, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(horizontal = 16.dp))
                    if (n.attachments.isNotEmpty()) {
                        LazyRow(contentPadding = PaddingValues(horizontal = 16.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            items(n.attachments, key = { it.id }) { a ->
                                Text(
                                    "📎 ${a.filename} · ${fmtSize(a.size_bytes)}",
                                    style = MaterialTheme.typography.bodySmall,
                                    maxLines = 1,
                                    overflow = TextOverflow.Ellipsis,
                                    modifier = Modifier.clip(RoundedCornerShape(999.dp)).background(MaterialTheme.colorScheme.surfaceVariant).padding(horizontal = 12.dp, vertical = 6.dp),
                                )
                            }
                        }
                    }
                    HtmlPanel(server, n.body_html, Modifier.weight(1f).fillMaxWidth())
                }
            }
        }
    }

    if (deleting) {
        AlertDialog(
            onDismissRequest = { deleting = false },
            title = { Text("Delete note?") },
            text = { Text("Deletes the note and its attachments.") },
            confirmButton = {
                TextButton(onClick = {
                    deleting = false
                    scope.launch {
                        runCatching { withContext(Dispatchers.IO) { api.deleteNote(projectID, noteID) } }
                        onBack()
                    }
                }) { Text("Delete") }
            },
            dismissButton = { TextButton(onClick = { deleting = false }) { Text("Cancel") } },
        )
    }
}