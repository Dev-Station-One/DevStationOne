package dev.devstationone.ui

import android.content.ContentValues
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.provider.MediaStore
import android.provider.OpenableColumns
import android.webkit.WebView
import android.widget.Toast
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.compose.BackHandler
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.horizontalScroll
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
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
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
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.core.content.FileProvider
import dev.devstationone.data.NoteDraftStore
import dev.devstationone.data.Server
import dev.devstationone.net.Api
import dev.devstationone.net.JournalAttachment
import dev.devstationone.net.JournalNote
import dev.devstationone.net.JournalNoteSummary
import dev.devstationone.ui.kit.MuxIcons
import dev.devstationone.ui.kit.MuxCard
import dev.devstationone.ui.kit.RoundIconButton
import dev.devstationone.ui.kit.ScreenHeader
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File
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

internal fun fmtSize(n: Long): String = when {
    n >= 1_048_576 -> "%.1f MB".format(n / 1_048_576.0)
    n >= 1024 -> "%.0f KB".format(n / 1024.0)
    else -> "$n B"
}

/**
 * The attachment filename comes from the server (or another client's upload);
 * strip separators before it becomes a cache-file path. ponytail: replace-only,
 * no allowlist — file names never flow into anything richer than a share URI.
 */
internal fun safeAttName(name: String): String =
    name.replace(Regex("[/\\\\\u0000]"), "_").ifBlank { "attachment" }

private val ATT_MAX_BYTES = 25 * 1024 * 1024
private const val ATT_MAX_PER_NOTE = 10
private val ATT_MIME_FALLBACK = "application/octet-stream"

/** Display name of a document-provider URI, for uploads. */
private fun queryName(res: android.content.ContentResolver, uri: Uri): String? =
    res.query(uri, null, null, null, null)?.use { c ->
        val i = c.getColumnIndex(OpenableColumns.DISPLAY_NAME)
        if (i >= 0 && c.moveToFirst()) c.getString(i) else null
    }

/** One toolbar pill in note edit mode. */
@Composable
private fun Tool(label: String, onClick: () -> Unit) {
    Text(
        label,
        fontSize = 13.sp,
        modifier = Modifier
            .clip(RoundedCornerShape(8.dp))
            .background(MaterialTheme.colorScheme.surfaceVariant)
            .clickable(onClick = onClick)
            .padding(horizontal = 10.dp, vertical = 6.dp),
    )
}

/**
 * The project Journal tab: a paged list of the project's notes. Rows carry
 * the server-provided snippet (HTML stripped server-side), the update time,
 * the attachment count and the linked commit when present. [onOpen] hands the
 * tapped note to the workspace; [onOpen] with `startEdit = true` (a newly
 * created note) opens it straight into edit mode.
 */
@Composable
fun JournalScreen(server: Server, projectID: String, onOpen: (JournalNoteSummary, Boolean) -> Unit) {
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

    fun create() {
        if (busy) return
        busy = true
        scope.launch {
            try {
                val n = withContext(Dispatchers.IO) { api.createNote(projectID) }
                val s = JournalNoteSummary(id = n.id)
                notes = listOf(s) + notes
                total += 1
                error = null
                onOpen(s, true)
            } catch (e: Exception) {
                error = "Create failed: ${e.message?.take(200)}"
            }
            busy = false
        }
    }

    Box(Modifier.fillMaxSize()) {
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
                MuxCard(onClick = { onOpen(n, false) }) {
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
        ExtendedFloatingActionButton(
            onClick = { create() },
            modifier = Modifier.align(Alignment.BottomEnd).padding(16.dp),
        ) { Text("New note") }
    }
}

/**
 * One journal note: attachment chips, the rendered (DOMPurify-sanitized)
 * body, edit mode (contenteditable WebView + native execCommand toolbar),
 * and a menu with delete.
 */
@OptIn(ExperimentalFoundationApi::class) // combinedClickable on attachment chips
@Composable
fun NoteScreen(server: Server, projectID: String, projectName: String, noteID: String, startEdit: Boolean = false, onBack: () -> Unit) {
    val scope = rememberCoroutineScope()
    val api = remember { Api(server) }
    val ctx = LocalContext.current
    val drafts = remember(projectID) { NoteDraftStore(ctx, projectID) }
    val webView = remember { mutableStateOf<WebView?>(null) }
    var note by remember { mutableStateOf<JournalNote?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var menu by remember { mutableStateOf(false) }
    var deleting by remember { mutableStateOf(false) }
    var editing by remember { mutableStateOf(false) }
    var saving by remember { mutableStateOf(false) }
    var linkDialog by remember { mutableStateOf(false) }
    var uploading by remember { mutableStateOf(false) }

    // ---- attachments (stage 3): upload via picker or camera, tap chip = share, long-press = save ----

    suspend fun reattach() {
        try {
            note = withContext(Dispatchers.IO) { api.note(projectID, noteID) }
        } catch (e: Exception) {
            error = "Refresh failed: ${e.message?.take(200)}"
        }
    }

    fun upload(uri: Uri, name: String, mime: String) {
        val n = note ?: return
        if (n.attachments.size >= ATT_MAX_PER_NOTE) {
            error = "A note can have at most $ATT_MAX_PER_NOTE attachments."
            return
        }
        uploading = true
        scope.launch {
            try {
                val bytes = withContext(Dispatchers.IO) { ctx.contentResolver.openInputStream(uri)?.use { it.readBytes() } }
                when {
                    bytes == null -> error = "Could not read the selected file."
                    bytes.size > ATT_MAX_BYTES -> error = "Attachment exceeds the 25 MB limit."
                    else -> {
                        withContext(Dispatchers.IO) { api.uploadAttachment(projectID, noteID, name, mime, bytes) }
                        reattach()
                    }
                }
            } catch (e: Exception) {
                error = "Upload failed: ${e.message?.take(200)}"
            }
            uploading = false
        }
    }

    fun share(a: JournalAttachment) {
        scope.launch {
            try {
                val f = File(File(ctx.cacheDir, "share").apply { mkdirs() }, a.id + "-" + safeAttName(a.filename))
                withContext(Dispatchers.IO) { f.outputStream().use { api.downloadAttachment(projectID, noteID, a.id, it) } }
                val uri = FileProvider.getUriForFile(ctx, "dev.devstationone.fileprovider", f)
                val i = Intent(Intent.ACTION_SEND).apply {
                    type = a.content_type.ifBlank { ATT_MIME_FALLBACK }
                    putExtra(Intent.EXTRA_STREAM, uri)
                    putExtra(Intent.EXTRA_TITLE, a.filename)
                    addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
                }
                ctx.startActivity(Intent.createChooser(i, "Share attachment"))
            } catch (e: Exception) {
                error = "Download failed: ${e.message?.take(200)}"
            }
        }
    }

    /** Writes the attachment to MediaStore's Downloads collection; API 29+ only (checked in [save]). */
    fun save(a: JournalAttachment) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) {
            error = "Saving to Downloads needs Android 10+; share it instead." // ponytail: pre-Q needs WRITE_EXTERNAL_STORAGE, not worth it
            return
        }
        scope.launch {
            try {
                val values = ContentValues().apply {
                    put(MediaStore.Downloads.DISPLAY_NAME, safeAttName(a.filename))
                    put(MediaStore.Downloads.MIME_TYPE, a.content_type.ifBlank { ATT_MIME_FALLBACK })
                    put(MediaStore.Downloads.IS_PENDING, 1)
                }
                val uri = withContext(Dispatchers.IO) { ctx.contentResolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values) }
                    ?: throw Exception("MediaStore refused the insert")
                withContext(Dispatchers.IO) { ctx.contentResolver.openOutputStream(uri)!!.use { api.downloadAttachment(projectID, noteID, a.id, it) } }
                values.clear()
                values.put(MediaStore.Downloads.IS_PENDING, 0)
                ctx.contentResolver.update(uri, values, null, null)
                Toast.makeText(ctx, "Saved to Downloads", Toast.LENGTH_SHORT).show()
            } catch (e: Exception) {
                error = "Save failed: ${e.message?.take(200)}"
            }
        }
    }

    val cameraFile = remember { File(File(ctx.cacheDir, "camera").apply { mkdirs() }, "capture.jpg") }
    val cameraUri = remember {
        FileProvider.getUriForFile(ctx, "dev.devstationone.fileprovider", cameraFile)
    }
    val photoName = remember { "photo-${System.currentTimeMillis()}.jpg" }
    val camera = rememberLauncherForActivityResult(ActivityResultContracts.TakePicture()) { ok ->
        if (ok) upload(cameraUri, photoName, "image/jpeg")
    }
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        if (uri != null) upload(uri, queryName(ctx.contentResolver, uri) ?: "file", ctx.contentResolver.getType(uri).orEmpty())
    }

    fun attachCamera() {
        try {
            camera.launch(cameraUri)
        } catch (e: Exception) {
            error = "No camera app available." // ActivityNotFoundException on camera-less devices
        }
    }

    // Latest sanitized snapshot pushed while editing — the draft. Seeded from
    // prefs on load so re-entering edit resumes where process death left off;
    // the page's debounced pushes keep this and the store fresh (NFR-3).
    var draft by remember { mutableStateOf<String?>(null) }

    LaunchedEffect(noteID) {
        error = null
        try {
            val n = withContext(Dispatchers.IO) { api.note(projectID, noteID) }
            note = n
            draft = drafts.load(noteID)
            if (startEdit) editing = true
        } catch (e: Exception) {
            error = "Note failed to load: ${e.message?.take(200)}"
        }
    }

    fun exec(cmd: String, value: String? = null) {
        val v = webView.value ?: return
        v.requestFocus() // keep the DOM selection: toolbar taps moved focus out of the WebView
        v.evaluateJavascript(
            "exec('${jsString(cmd)}', ${value?.let { "'${jsString(it)}'" } ?: "null"})",
            null,
        )
    }

    fun save() {
        val n = note ?: return
        saving = true
        scope.launch {
            try {
                // Flush the debounced push before reading the draft. ponytail:
                // fixed quiet period instead of a synchronous extract round trip;
                // race window is the flush-to-bridge hop, not user-visible.
                webView.value?.evaluateJavascript("push()", null)
                delay(150)
                val html = draft ?: n.body_html
                val updated = withContext(Dispatchers.IO) { api.putNote(projectID, noteID, html) }
                drafts.delete(noteID) // removed only once the server save succeeded
                draft = null
                note = updated
                editing = false
            } catch (e: Exception) {
                error = "Save failed: ${e.message?.take(200)}" // draft stays, re-edit resumes
            }
            saving = false
        }
    }

    BackHandler(enabled = editing) { editing = false }
    BackHandler(enabled = !editing) { onBack() }

    Scaffold { pad ->
        Column(Modifier.padding(pad).fillMaxSize()) {
            ScreenHeader(
                title = projectName,
                onBack = { if (editing) editing = false else onBack() },
                actions = {
                    if (editing) {
                        TextButton(enabled = !saving, onClick = { save() }) { Text(if (saving) "Saving…" else "Save") }
                    } else {
                        TextButton(onClick = { editing = true }) { Text("Edit") }
                        Box {
                            RoundIconButton(MuxIcons.More, "Menu") { menu = true }
                            DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                                DropdownMenuItem(text = { Text("Take photo…") }, onClick = { menu = false; attachCamera() })
                                DropdownMenuItem(text = { Text("Add file…") }, onClick = { menu = false; picker.launch(arrayOf("*/*")) })
                                DropdownMenuItem(text = { Text("Delete note…") }, onClick = { menu = false; deleting = true })
                            }
                        }
                    }
                },
            )
            val n = note
            when {
                error != null -> Text(error.orEmpty(), color = MaterialTheme.colorScheme.error, fontSize = 12.sp, modifier = Modifier.padding(16.dp))
                n == null -> CircularProgressIndicator(Modifier.padding(24.dp))
                else -> {
                    if (editing) {
                        Row(
                            Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(horizontal = 8.dp),
                            horizontalArrangement = Arrangement.spacedBy(2.dp),
                        ) {
                            Tool("B") { exec("bold") }
                            Tool("I") { exec("italic") }
                            Tool("U") { exec("underline") }
                            Tool("H2") { exec("formatBlock", "<H2>") }
                            Tool("H3") { exec("formatBlock", "<H3>") }
                            Tool("• list") { exec("insertUnorderedList") }
                            Tool("1. list") { exec("insertOrderedList") }
                            Tool("quote") { exec("formatBlock", "<BLOCKQUOTE>") }
                            Tool("code") { exec("formatBlock", "<PRE>") }
                            Tool("link") { linkDialog = true }
                            Tool("clear") { exec("removeFormat") }
                        }
                        // last-write-wins is the policy (§4); make it observable instead of silent
                        Text(
                            "editing · draft kept until saved · last updated ${fmt(n.updated_at)}",
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp),
                        )
                    } else {
                        val meta = buildString {
                            append(fmt(n.created_at))
                            append(if (n.creator_name.isBlank()) "" else " · ${n.creator_name}")
                            append(" · updated ${fmt(n.updated_at)}")
                            if (n.commit_sha.isNotBlank()) append(" · ${n.commit_sha.take(7)} ${n.commit_subject}")
                        }
                        Text(meta, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(horizontal = 16.dp))
                    }
                    if (uploading) {
                        Text(
                            "Uploading…",
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp),
                        )
                    }
                    if (n.attachments.isNotEmpty()) {
                        LazyRow(contentPadding = PaddingValues(horizontal = 16.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            items(n.attachments, key = { it.id }) { a ->
                                Text(
                                    "📎 ${a.filename} · ${fmtSize(a.size_bytes)}",
                                    style = MaterialTheme.typography.bodySmall,
                                    maxLines = 1,
                                    overflow = TextOverflow.Ellipsis,
                                    modifier = Modifier
                                        .clip(RoundedCornerShape(999.dp))
                                        .background(MaterialTheme.colorScheme.surfaceVariant)
                                        .combinedClickable(onClick = { share(a) }, onLongClick = { save(a) })
                                        .padding(horizontal = 12.dp, vertical = 6.dp),
                                )
                            }
                        }
                    }
                    HtmlPanel(
                        server,
                        if (editing) (draft ?: n.body_html) else n.body_html,
                        Modifier.weight(1f).fillMaxWidth(),
                        editing = editing,
                        onHtml = { text ->
                            draft = text
                            drafts.save(noteID, text)
                        },
                        webViewRef = webView,
                    )
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

    if (linkDialog) {
        var linkUrl by remember { mutableStateOf("") }
        AlertDialog(
            onDismissRequest = { linkDialog = false },
            title = { Text("Insert link") },
            text = { OutlinedTextField(value = linkUrl, onValueChange = { linkUrl = it }, label = { Text("URL") }, singleLine = true) },
            confirmButton = {
                TextButton(onClick = {
                    linkDialog = false
                    if (linkUrl.isNotBlank()) exec("createLink", linkUrl)
                }) { Text("Insert") }
            },
            dismissButton = { TextButton(onClick = { linkDialog = false }) { Text("Cancel") } },
        )
    }
}