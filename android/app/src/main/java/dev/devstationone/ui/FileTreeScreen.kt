package dev.devstationone.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import dev.devstationone.data.Server
import dev.devstationone.net.Api
import dev.devstationone.net.FileInfo
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** Dirs first, then case-insensitive by name — the order the web tree uses. */
internal fun dirsFirst(entries: List<FileInfo>): List<FileInfo> =
    entries.sortedWith(compareBy({ it.type != "dir" }, { it.name.lowercase() }, { it.name }))

/** Breadcrumb labels for a path; "." (and an empty path) is the project root. */
internal fun crumbs(path: String): List<String> =
    if (path == "." || path.isEmpty()) listOf("root")
    else listOf("root") + path.split('/')

/** The path at breadcrumb [index] (0 = root, so "."), or null when outside. */
internal fun subPath(path: String, index: Int): String? {
    if (index < 0 || index >= crumbs(path).size) return null
    if (index == 0) return "."
    return path.split('/').take(index).joinToString("/")
}

/** One glyph per entry type, matching the emoji style the Journal uses. */
internal fun fileGlyph(type: String): String = when (type) {
    "dir" -> "📁"
    "symlink" -> "🔗"
    else -> "📄"
}

/**
 * The project Files tab: breadcrumb drill-in over the runtime filesystem
 * (read-only in round 1 until the stage-5 editor lands) plus a name search
 * over the subtree rooted at the current directory. Search results are
 * root-relative paths; they open in the code editor once stage 5 ships.
 */
@Composable
fun FileTreeScreen(server: Server, projectID: String) {
    val scope = rememberCoroutineScope()
    val api = remember { Api(server) }
    var path by remember { mutableStateOf(".") }
    var entries by remember { mutableStateOf<List<FileInfo>?>(null) }
    var query by remember { mutableStateOf("") }
    var searching by remember { mutableStateOf(false) }
    // Non-null means search mode (results shown instead of the listing).
    var results by remember { mutableStateOf<List<String>?>(null) }
    var error by remember { mutableStateOf<String?>(null) }

    fun loadDir(p: String) {
        path = p
        results = null
        query = ""
        scope.launch {
            try {
                val e = withContext(Dispatchers.IO) { api.listFiles(projectID, p) }
                entries = e
                error = null
            } catch (e: Exception) {
                error = "Listing failed: ${e.message?.take(200)}"
            }
        }
    }

    fun search(q: String) {
        if (q.isBlank()) return
        searching = true
        scope.launch {
            try {
                results = withContext(Dispatchers.IO) { api.searchFiles(projectID, q.trim(), path) }
                error = null
            } catch (e: Exception) {
                error = "Search failed: ${e.message?.take(200)}"
            }
            searching = false
        }
    }
    LaunchedEffect(projectID) { loadDir(path) }

    Column(Modifier.fillMaxSize()) {
        Text(
            "Files — live from the project runtime",
            style = MaterialTheme.typography.labelLarge,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(horizontal = 16.dp, vertical = 10.dp),
        )
        // Breadcrumbs: root / dir / dir, tap any to jump back up.
        Row(
            Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(horizontal = 16.dp),
            horizontalArrangement = Arrangement.spacedBy(4.dp),
        ) {
            crumbs(path).forEachIndexed { i, c ->
                Text(
                    c,
                    fontSize = 13.sp,
                    color = if (i == crumbs(path).size - 1) MaterialTheme.colorScheme.onSurface else MaterialTheme.colorScheme.primary,
                    modifier = Modifier.clickable(enabled = i < crumbs(path).size - 1) { subPath(path, i)?.let { loadDir(it) } },
                )
                Text("  /  ", fontSize = 13.sp, color = MaterialTheme.colorScheme.outline)
            }
        }
        OutlinedTextField(
            value = query,
            onValueChange = { query = it },
            label = { Text("Search names under this directory") },
            singleLine = true,
            modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp),
            keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
            keyboardActions = KeyboardActions(onSearch = { search(query) }),
        )
        error?.let {
            Text(it, color = MaterialTheme.colorScheme.error, fontSize = 12.sp, modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp))
        }
        when {
            results != null -> LazyColumn(Modifier.weight(1f), contentPadding = PaddingValues(horizontal = 16.dp, vertical = 4.dp)) {
                items(results.orEmpty()) { r ->
                    Text(
                        r,
                        fontSize = 14.sp,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.padding(vertical = 6.dp),
                    )
                }
                if (results.orEmpty().isEmpty() && !searching) {
                    item { Text("No matches.", color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(top = 8.dp)) }
                }
            }
            entries == null -> CircularProgressIndicator(Modifier.padding(24.dp))
            else -> LazyColumn(Modifier.weight(1f), contentPadding = PaddingValues(horizontal = 16.dp, vertical = 4.dp)) {
                val l = dirsFirst(entries.orEmpty())
                items(l, key = { "${it.type}:${it.name}" }) { f ->
                    Row(
                        Modifier
                            .fillMaxWidth()
                            .clickable(enabled = f.type == "dir") { loadDir(if (path == ".") f.name else "$path/${f.name}") }
                            .padding(vertical = 6.dp),
                        horizontalArrangement = Arrangement.spacedBy(8.dp),
                    ) {
                        Text(fileGlyph(f.type))
                        Text(f.name, fontSize = 14.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        if (f.type != "dir") Text(" · ${fmtSize(f.size)}", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                }
                if (l.isEmpty()) {
                    item { Text("Empty directory.", color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(top = 8.dp)) }
                }
                item {
                    TextButton(enabled = !searching, onClick = { search(query) }, modifier = Modifier.fillMaxWidth()) {
                        Text(if (searching) "Searching…" else "Search")
                    }
                }
            }
        }
    }
}