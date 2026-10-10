package dev.devstationone.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.viewinterop.AndroidView
import dev.devstationone.data.FileDraftStore
import dev.devstationone.data.Server
import dev.devstationone.net.Api
import dev.devstationone.ui.kit.ScreenHeader
import io.github.rosemoe.sora.event.ContentChangeEvent
import io.github.rosemoe.sora.langs.textmate.TextMateColorScheme
import io.github.rosemoe.sora.langs.textmate.TextMateLanguage
import io.github.rosemoe.sora.langs.textmate.registry.FileProviderRegistry
import io.github.rosemoe.sora.langs.textmate.registry.GrammarRegistry
import io.github.rosemoe.sora.langs.textmate.registry.ThemeRegistry
import io.github.rosemoe.sora.langs.textmate.registry.model.ThemeModel
import io.github.rosemoe.sora.langs.textmate.registry.provider.AssetsFileResolver
import io.github.rosemoe.sora.widget.CodeEditor
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.eclipse.tm4e.core.registry.IThemeSource

/** Editor transfer guards (DESIGN_ANDROID_PARITY.md §4): hard ceiling, soft warn, binary sniff. */
internal const val EDITOR_MAX_BYTES = 4 * 1024 * 1024
internal const val EDITOR_WARN_BYTES = 1024 * 1024

/**
 * The §4 collision table. [baseline] is the mtime stat'ed at open, [now] a
 * fresh stat just before saving, [edited] whether the user changed the
 * buffer. "put" saves freely; "reload" means the file changed out from
 * under an untouched editor; "conflict" means both sides moved.
 */
internal fun saveDecision(baseline: String, now: String, edited: Boolean): String = when {
    now == baseline -> "put"
    !edited -> "reload"
    else -> "conflict"
}

/**
 * Extension → TextMate scope name for the vendored grammar subset
 * (assets/textmate). Null means the default plain-text language. A leading
 * dot is fine ("." names hidden files like ".gitignore" via the fallback).
 */
internal fun grammarFor(fileName: String): String? {
    if (fileName.substringAfterLast('/').equals("dockerfile", ignoreCase = true)) return "source.dockerfile"
    return when (fileName.substringAfterLast('.', "").lowercase()) {
        "kt", "kts" -> "source.kotlin"
        "java" -> "source.java"
        "json" -> "source.json"
        "yaml", "yml" -> "source.yaml"
        "go" -> "source.go"
        "ts", "tsx" -> "source.ts"
        "js", "jsx", "mjs", "cjs" -> "source.js"
        "css" -> "source.css"
        "html", "htm" -> "text.html.basic"
        "md", "markdown" -> "text.html.markdown"
        "sh", "bash", "zsh" -> "source.shell"
        "toml" -> "source.toml"
        "ini", "cfg", "conf" -> "source.ini"
        "c", "h" -> "source.c"
        "cpp", "cc", "cxx", "hpp" -> "source.cpp"
        "cs", "csx" -> "source.cs"
        "php" -> "source.php"
        "rs" -> "source.rust"
        "sql" -> "source.sql"
        else -> null
    }
}

/** A NUL byte in the first 8 KiB is good enough evidence of a binary file. */
internal fun looksBinary(bytes: ByteArray): Boolean {
    if (bytes.isEmpty()) return false
    return bytes.take(8192).contains(0.toByte())
}

// TextMate registries are process-wide statics; one-time setup, guarded.
@Volatile private var textmateReady = false
private val textmateLock = Any()

private fun setUpTextmate(ctx: android.content.Context) {
    if (textmateReady) return
    synchronized(textmateLock) {
        if (textmateReady) return
        FileProviderRegistry.getInstance().addFileProvider(AssetsFileResolver(ctx.assets))
        ThemeRegistry.getInstance().loadTheme(
            ThemeModel(
                IThemeSource.fromInputStream(
                    FileProviderRegistry.getInstance().tryGetInputStream("textmate/darcula.json"),
                    "textmate/darcula.json", null,
                ),
                "darcula",
            ).apply { isDark = true }
        )
        GrammarRegistry.getInstance().loadGrammars("textmate/languages.json")
        textmateReady = true
    }
}

/**
 * One project file in the sora-editor instance: load with the §4 guards
 * (stat baseline, 4 MB refusal, 1 MB warning, NUL-byte binary sniff),
 * edit, save via the collision table, keep the dirty buffer in
 * [FileDraftStore] on pause/back so a crash resumes. Read-only surfaces
 * (over-limit, binary) explain themselves instead of pretending.
 */
@Composable
fun CodeScreen(server: Server, projectID: String, projectName: String, filePath: String, onBack: () -> Unit) {
    val scope = rememberCoroutineScope()
    val api = remember { Api(server) }
    val ctx = androidx.compose.ui.platform.LocalContext.current
    val drafts = remember(server.id, projectID) { FileDraftStore(ctx, server.id, projectID) }
    val haptic = LocalHapticFeedback.current

    var loading by remember { mutableStateOf(true) }
    var error by remember { mutableStateOf<String?>(null) }
    var notice by remember { mutableStateOf<String?>(null) } // read-only refusal (too big / binary)
    var refuser by remember { mutableStateOf<(() -> Unit)?>(null) } // dialog confirm → continue load
    var dialog by remember { mutableStateOf<String?>(null) } // "reload" | "conflict"
    var dirty by remember { mutableStateOf(false) }
    var saving by remember { mutableStateOf(false) }
    val baseline = remember { mutableStateOf("") }
    val editor = remember { mutableStateOf<CodeEditor?>(null) }

    // Text waiting for the CodeEditor. load() can finish before AndroidView's
    // factory has created the view (two RPCs can beat one layout pass), so it
    // parks here and the AndroidView update() applies it once the editor exists.
    var pendingText by remember { mutableStateOf<String?>(null) }
    var pendingDirty by remember { mutableStateOf(false) }

    // ---- load (stat → guards → read → render) ----
    fun load(warnOk: Boolean = false) {
        scope.launch {
            loading = true
            error = null
            notice = null
            try {
                val st = withContext(Dispatchers.IO) { api.statFile(projectID, filePath) }
                baseline.value = st.mtime
                when {
                    (st.type == "dir") -> notice = "That is a directory, not a file."
                    st.size > EDITOR_MAX_BYTES -> notice =
                        "This file is ${fmtSize(st.size)} — the phone editor tops out at 4 MB. Open it in the web UI instead."
                    st.size > EDITOR_WARN_BYTES && !warnOk -> refuser = { load(warnOk = true) }
                    else -> {
                        val bytes = withContext(Dispatchers.IO) { api.readFile(projectID, filePath) }
                        if (looksBinary(bytes)) {
                            notice = "This looks like a binary file — edit it on the web instead."
                        } else {
                            val text = drafts.load(filePath) // resume a dirty draft (NFR-3) over the fetched copy
                            pendingText = text ?: bytes.decodeToString()
                            pendingDirty = text != null
                        }
                    }
                }
            } catch (e: Exception) {
                error = "Could not open ${e.message?.take(150)}" // never echo file contents into the UI
            }
            loading = false
        }
    }
    LaunchedEffect(filePath) { load() }

    // ---- save (§4 table) ----
    fun doSave() {
        val e = editor.value ?: return
        val bytes = e.text.toString().toByteArray()
        saving = true
        scope.launch {
            try {
                withContext(Dispatchers.IO) { api.writeFile(projectID, filePath, bytes) }
                baseline.value = withContext(Dispatchers.IO) { api.statFile(projectID, filePath).mtime }
                drafts.delete(filePath)
                dirty = false
                haptic.performHapticFeedback(HapticFeedbackType.TextHandleMove) // save confirmed, no dialog needed
            } catch (ex: Exception) {
                error = "Save failed: ${ex.message?.take(150)}" // draft stays
            }
            saving = false
        }
    }

    fun save() {
        if (saving) return
        scope.launch {
            val now = try { withContext(Dispatchers.IO) { api.statFile(projectID, filePath).mtime } } catch (e: Exception) { null }
            when {
                now == null -> error = "Save failed: could not reach the runtime."
                else -> when (saveDecision(baseline.value, now, dirty)) {
                    "put" -> doSave()
                    "reload" -> dialog = "reload" // file changed out from under an untouched editor
                    else -> dialog = "conflict" // both sides moved
                }
            }
        }
    }

    fun onClose() {
        val e = editor.value
        if (dirty && e != null) drafts.save(filePath, e.text.toString()) // §6: back-nav of a dirty screen persists the buffer
        onBack()
    }
    BackHandler { onClose() }
    val lifecycleOwner = LocalLifecycleOwner.current // captured before the effect: composable context only there
    DisposableEffect(filePath) { // §6: app-pause also persists
        val obs = androidx.lifecycle.LifecycleEventObserver { _, ev ->
            if (ev == androidx.lifecycle.Lifecycle.Event.ON_STOP) {
                editor.value?.takeIf { dirty }?.let { drafts.save(filePath, it.text.toString()) }
            }
        }
        lifecycleOwner.lifecycle.addObserver(obs)
        onDispose { lifecycleOwner.lifecycle.removeObserver(obs) }
    }

    Surface {
        Column {
            ScreenHeader(
                title = filePath.substringAfterLast('/'),
                onBack = { onClose() },
                actions = {
                    Text(
                        if (dirty) "●" else "",
                        color = MaterialTheme.colorScheme.primary,
                        modifier = Modifier.padding(end = 8.dp),
                    )
                    TextButton(enabled = !saving && !loading && notice == null, onClick = { save() }) {
                        Text(if (saving) "Saving…" else "Save")
                    }
                    // §3 top bar: reload re-GETs; confirm only when it would drop edits
                    TextButton(enabled = !saving && !loading && notice == null, onClick = {
                        if (dirty) dialog = "conflict-reload"
                        else load()
                    }) { Text("Reload") }
                },
            )
            when {
                notice != null -> Text(
                    notice.orEmpty(),
                    fontSize = 15.sp,
                    modifier = Modifier.padding(24.dp),
                )
                error != null -> Text(
                    error.orEmpty(),
                    color = MaterialTheme.colorScheme.error,
                    fontSize = 12.sp,
                    modifier = Modifier.padding(16.dp),
                )
                loading -> CircularProgressIndicator(Modifier.padding(24.dp))
                else -> AndroidView(
                    modifier = Modifier.fillMaxSize(),
                    factory = { c ->
                        setUpTextmate(c)
                        CodeEditor(c).apply {
                            colorScheme = TextMateColorScheme.create(ThemeRegistry.getInstance())
                            isWordwrap = false
                            // one dirty hook: every content mutation marks the buffer
                            subscribeEvent(ContentChangeEvent::class.java) { _, _ -> dirty = true }
                            editor.value = this
                        }
                    },
                    update = { e ->
                        // deliver whatever load() parked (initial load, reload, draft resume)
                        pendingText?.let { t ->
                            e.setText(t)
                            dirty = pendingDirty
                            pendingDirty = false
                            pendingText = null
                            grammarFor(filePath)?.let { scopeName ->
                                e.setEditorLanguage(TextMateLanguage.create(scopeName, false))
                            }
                        }
                    },
                    onRelease = { it.release() },
                )
            }
        }
    }

    if (refuser != null) { // 1 MB+ warning (§4): proceed or back out
        AlertDialog(
            onDismissRequest = { refuser = null },
            title = { Text("Large file") },
            text = { Text("This file is over 1 MB — editing it on a phone may be slow. Continue?") },
            confirmButton = { TextButton(onClick = { val f = refuser; refuser = null; f?.invoke() }) { Text("Open anyway") } },
            dismissButton = { TextButton(onClick = { refuser = null; onBack() }) { Text("Back") } },
        )
    }

    if (dialog == "reload") {
        AlertDialog(
            onDismissRequest = { dialog = null },
            title = { Text("File changed") },
            text = { Text("The file changed since you opened it, and your copy has no edits. Reload?") },
            confirmButton = { TextButton(onClick = { dialog = null; load() }) { Text("Reload") } },
            dismissButton = { TextButton(onClick = { dialog = null; onBack() }) { Text("Back") } },
        )
    }

    if (dialog == "conflict") {
        AlertDialog(
            onDismissRequest = { dialog = null },
            title = { Text("Conflict") },
            text = { Text("The file changed on the server while you were editing. Keep your version, or dump your edits and reload?") },
            confirmButton = { TextButton(onClick = { dialog = null; doSave() }) { Text("Keep mine (save anyway)") } },
            dismissButton = { TextButton(onClick = { dialog = null; load() }) { Text("Reload theirs") } },
        )
    }

    if (dialog == "conflict-reload") {
        AlertDialog(
            onDismissRequest = { dialog = null },
            title = { Text("Discard edits?") },
            text = { Text("Reloading drops your unsaved edits (a draft is kept only when you leave via Back).") },
            confirmButton = { TextButton(onClick = { dialog = null; load() }) { Text("Discard and reload") } },
            dismissButton = { TextButton(onClick = { dialog = null }) { Text("Cancel") } },
        )
    }
}