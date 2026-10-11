package dev.devstationone.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
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
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import dev.devstationone.data.Server
import dev.devstationone.net.Api
import dev.devstationone.net.GitBranchInfo
import dev.devstationone.net.GitBranchStatus
import dev.devstationone.net.GitCommitInfo
import dev.devstationone.net.GitFileStatus
import dev.devstationone.net.GitStatus
import dev.devstationone.ui.kit.ScreenHeader
import dev.devstationone.ui.kit.TabChipColors
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

// ---- pure status→group mapping (unit-tested; never re-parses git text) ----

/** One list row: the file plus its displayed status chip (a staged/unstaged XY half, "?", or "U"). */
data class ScmRow(val file: GitFileStatus, val chip: String) {
    /** "old → new" for renames; the path otherwise. */
    val label: String = if (file.orig_path.isEmpty()) file.path else "${file.orig_path} → ${file.path}"
}

data class ScmGroups(val staged: List<ScmRow>, val changed: List<ScmRow>)

/**
 * Porcelain-v2 XY pairs → the two UI groups (US-SC1). Untracked ("?"), a
 * staged half and an unstaged half of one file each get their own row in the
 * group that half belongs to; an unmerged pair ("U") shows once in Changes.
 */
internal fun groupScm(files: List<GitFileStatus>): ScmGroups {
    val staged = mutableListOf<ScmRow>()
    val changed = mutableListOf<ScmRow>()
    for (f in files) {
        when {
            f.index == "?" -> changed += ScmRow(f, "?") // untracked: nothing staged, listed under Changes
            f.index == "U" || f.worktree == "U" -> changed += ScmRow(f, "U") // merge conflict — one row
            else -> {
                if (f.index !in setOf(".", "?")) staged += ScmRow(f, f.index)
                if (f.worktree !in setOf(".", "?", "U")) changed += ScmRow(f, f.worktree)
            }
        }
    }
    return ScmGroups(staged, changed)
}

/** Pull/push need a remote to be worth anything (US-SC5: buttons reflect HasRemote). */
internal fun syncEnabled(branch: GitBranchStatus?): Boolean = branch?.has_remote == true

/** Commit needs a message; plain Commit needs the index, Commit-all needs any change at all. */
internal fun commitEnabled(groups: ScmGroups, all: Boolean, message: String): Boolean =
    message.isNotBlank() && (if (all) groups.staged.isNotEmpty() || groups.changed.isNotEmpty() else groups.staged.isNotEmpty())

/** Rows `git restore` can revert: tracked worktree changes only — untracked ("?") and conflicts ("U") are never offered (OQ-3). */
internal fun discardable(groups: ScmGroups): List<ScmRow> = groups.changed.filter { it.chip != "?" && it.chip != "U" }

/** OQ-4: branch switching is disabled while the status records any change (the runtime refuses too). */
internal fun checkoutEnabled(status: GitStatus?): Boolean = status != null && status.files.isEmpty()

/** Amend is always offered once a commit exists; the runtime is the authority on "already pushed" (OQ-5). */
internal fun amendEnabled(hasHistory: Boolean, message: String): Boolean = hasHistory && message.isNotBlank()

private val SCM_WHEN = DateTimeFormatter.ofPattern("MMM d, HH:mm").withZone(ZoneId.systemDefault())

private fun fmt(iso: String): String = try {
    SCM_WHEN.format(Instant.parse(iso))
} catch (_: Exception) {
    iso.take(10)
}

private val diffAddedColor = Color(TabChipColors["Green"]!!)
private val diffRemovedColor = Color(TabChipColors["Red"]!!)

/**
 * The Source Control tab (docs/DESIGN_ANDROID_SCM.md §4, slice 2): branch
 * header, refresh/pull/push, grouped status changes with per-row
 * stage/unstage, the commit box, and recent history. The branch sheet,
 * discard and amend ride slice 3. Request/response only — refresh is the
 * only way data updates (US-SC3).
 */
@Composable
fun ScmScreen(server: Server, projectID: String, onOpenDiff: (path: String, staged: Boolean) -> Unit) {
    val scope = rememberCoroutineScope()
    val api = remember { Api(server) }
    var status by remember { mutableStateOf<GitStatus?>(null) }
    var history by remember { mutableStateOf<List<GitCommitInfo>>(emptyList()) }
    var message by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    var notice by remember { mutableStateOf<String?>(null) }
    var conflicts by remember { mutableStateOf<List<GitFileStatus>?>(null) }
    var discarding by remember { mutableStateOf<List<String>?>(null) }
    var amending by remember { mutableStateOf(false) }
    var branches by remember { mutableStateOf<List<GitBranchInfo>?>(null) }
    var newBranch by remember { mutableStateOf<String?>(null) }

    fun refresh() {
        if (busy) return
        busy = true
        scope.launch {
            try {
                val s = withContext(Dispatchers.IO) { api.gitStatus(projectID) }
                status = s
                history = withContext(Dispatchers.IO) { api.gitLog(projectID) }
                error = null
            } catch (e: Exception) {
                error = "Source Control failed to load: ${e.message?.take(200)}"
            }
            busy = false
        }
    }
    LaunchedEffect(projectID) { refresh() }

    /** Runs one mutating action; refusals (502s) surface verbatim — the runtime is the authority. */
    fun act(label: String, block: suspend () -> String?, onDone: () -> Unit = {}) {
        if (busy) return
        busy = true
        scope.launch {
            try {
                notice = withContext(Dispatchers.IO) { block() }
                error = null
                onDone()
            } catch (e: Exception) {
                notice = null
                error = "$label failed: ${e.message?.take(200)}"
            }
            busy = false
            refresh()
        }
    }

    fun pull() {
        if (busy) return
        busy = true
        scope.launch {
            try {
                val cs = withContext(Dispatchers.IO) { api.gitPull(projectID) }
                conflicts = if (cs.isEmpty()) null else cs // dialog only when there is something to say
                notice = if (cs.isEmpty()) "Pulled" else null
                error = null
            } catch (e: Exception) {
                error = "Pull failed: ${e.message?.take(200)}"
            }
            busy = false
            refresh()
        }
    }

    val groups = status?.let { groupScm(it.files) } ?: ScmGroups(emptyList(), emptyList())

    fun commit(all: Boolean) {
        if (!commitEnabled(groups, all, message)) return
        val msg = message.trim()
        act(
            if (all) "Commit all" else "Commit",
            {
                if (all) groups.changed.forEach { api.gitStage(projectID, it.file.path) }
                api.gitCommit(projectID, msg)
                "Committed"
            },
            onDone = { message = "" },
        )
    }

    LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 16.dp)) {
        item {
            val b = status?.branch
            Text(
                when {
                    b == null -> "Source Control"
                    b.name.isBlank() -> "detached HEAD"
                    else -> buildString {
                        append(b.name)
                        if (b.upstream.isNotBlank()) append(" · ${b.upstream}")
                        if (b.ahead != 0 || b.behind != 0) append("  ↑${b.ahead} ↓${b.behind}")
                    }
                },
                style = MaterialTheme.typography.labelLarge,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(horizontal = 16.dp, vertical = 10.dp),
            )
        }
        item {
            Row(Modifier.fillMaxWidth().padding(horizontal = 8.dp), horizontalArrangement = Arrangement.spacedBy(2.dp)) {
                TextButton(enabled = !busy, onClick = { refresh() }) { Text("Refresh") }
                TextButton(enabled = !busy && status != null, onClick = {
                    act("Branches", { branches = api.gitBranches(projectID); null })
                }) { Text("Branch ▾") }
                Spacer(Modifier.width(8.dp))
                TextButton(enabled = !busy && syncEnabled(status?.branch), onClick = { pull() }) { Text("Pull") }
                TextButton(enabled = !busy && syncEnabled(status?.branch), onClick = {
                    act("Push", { if (api.gitPush(projectID)) "Published new branch" else "Pushed" })
                }) { Text("Push") }
            }
        }
        error?.let { e ->
            item { Text(e, color = MaterialTheme.colorScheme.error, fontSize = 12.sp, modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp)) }
        }
        notice?.takeIf { it.isNotEmpty() }?.let { n ->
            item { Text(n, color = MaterialTheme.colorScheme.primary, fontSize = 12.sp, modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp)) }
        }
        if (status == null && busy) {
            item { CircularProgressIndicator(Modifier.padding(top = 24.dp, start = 16.dp)) }
        } else {
            item {
                GroupHeader(
                    "Staged Changes (${groups.staged.size})",
                    allLabel = if (groups.staged.isEmpty()) null else "unstage all",
                    onAll = { act("Unstage", { groups.staged.forEach { api.gitUnstage(projectID, it.file.path) }; "" }) },
                )
            }
            items(groups.staged, key = { "s" + it.file.orig_path + "\n" + it.file.path }) { row ->
                ScmFileRow(
                    row = row,
                    trailing = "unstage",
                    onToggle = { act("Unstage", { api.gitUnstage(projectID, row.file.path); "" }) },
                    onOpen = { onOpenDiff(row.file.path, true) },
                )
            }
            item {
                GroupHeader(
                    "Changes (${groups.changed.size})",
                    allLabel = if (groups.changed.isEmpty()) null else "stage all",
                    onAll = { act("Stage", { groups.changed.forEach { api.gitStage(projectID, it.file.path) }; "" }) },
                    secondLabel = if (discardable(groups).isEmpty()) null else "discard all",
                    onSecond = { discarding = discardable(groups).map { it.file.path } },
                )
            }
            items(groups.changed, key = { "c" + it.file.orig_path + "\n" + it.file.path }) { row ->
                ScmFileRow(
                    row = row,
                    trailing = "stage",
                    onToggle = { act("Stage", { api.gitStage(projectID, row.file.path); "" }) },
                    onOpen = { onOpenDiff(row.file.path, false) },
                    onDiscard = if (row.chip == "?" || row.chip == "U") null else ({ discarding = listOf(row.file.path) }),
                )
            }
            item {
                Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 12.dp)) {
                    OutlinedTextField(
                        value = message,
                        onValueChange = { message = it },
                        modifier = Modifier.fillMaxWidth(),
                        placeholder = { Text(if (groups.staged.isEmpty()) "Commit message (stage first)" else "Commit message") },
                        minLines = 2,
                    )
                    Row(Modifier.padding(top = 4.dp)) {
                        TextButton(enabled = !busy && commitEnabled(groups, all = false, message), onClick = { commit(all = false) }) { Text("Commit") }
                        TextButton(enabled = !busy && commitEnabled(groups, all = true, message), onClick = { commit(all = true) }) { Text("Commit all") }
                        TextButton(enabled = !busy && amendEnabled(history.isNotEmpty(), message), onClick = { amending = true }) { Text("Amend…") }
                    }
                }
            }
            item {
                Text(
                    if (history.isEmpty()) "History" else "History (${history.size})",
                    style = MaterialTheme.typography.labelLarge,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp),
                )
            }
            items(history, key = { it.sha }) { c ->
                ScmCommitRow(c)
            }
            if (history.isEmpty() && !busy && error == null) {
                item { Text("No commits yet.", color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(horizontal = 16.dp)) }
            }
        }
    }

    discarding?.let { paths ->
        AlertDialog(
            onDismissRequest = { discarding = null },
            title = { Text("Discard changes?") },
            text = { Text("This permanently reverts unstaged changes in:\n\n" + paths.joinToString("\n") + "\n\nThere is no undo.") },
            confirmButton = {
                TextButton(onClick = {
                    discarding = null
                    act("Discard", { paths.forEach { api.gitDiscard(projectID, it) }; "Discarded" })
                }) { Text("Discard") }
            },
            dismissButton = { TextButton(onClick = { discarding = null }) { Text("Cancel") } },
        )
    }

    if (amending) {
        AlertDialog(
            onDismissRequest = { amending = false },
            title = { Text("Amend last commit?") },
            text = { Text("Replaces \"${history.firstOrNull()?.subject.orEmpty()}\" with a new commit carrying the staged changes and your message. Refused if it is already on the remote.") },
            confirmButton = {
                TextButton(onClick = {
                    amending = false
                    val msg = message.trim()
                    act("Amend", { api.gitAmend(projectID, msg); "Amended" }, onDone = { message = "" })
                }) { Text("Amend") }
            },
            dismissButton = { TextButton(onClick = { amending = false }) { Text("Cancel") } },
        )
    }

    branches?.let { list ->
        val canSwitch = checkoutEnabled(status)
        AlertDialog(
            onDismissRequest = { branches = null },
            title = { Text("Branches") },
            text = {
                LazyColumn {
                    if (!canSwitch) item {
                        Text("Commit or discard changes first to switch branches.", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(bottom = 8.dp))
                    }
                    items(list, key = { it.name }) { b ->
                        TextButton(
                            enabled = !b.remote && !b.current && canSwitch,
                            onClick = { branches = null; act("Checkout", { api.gitCheckout(projectID, b.name); "Switched to ${b.name}" }) },
                            modifier = Modifier.fillMaxWidth(),
                        ) {
                            Text((if (b.current) "✓ " else if (b.remote) "⇡ " else "") + b.name + if (b.upstream.isNotEmpty()) "  → ${b.upstream}" else "", maxLines = 1, overflow = TextOverflow.Ellipsis)
                        }
                    }
                }
            },
            confirmButton = { TextButton(onClick = { branches = null; newBranch = "" }) { Text("New branch…") } },
            dismissButton = { TextButton(onClick = { branches = null }) { Text("Close") } },
        )
    }

    newBranch?.let { name ->
        AlertDialog(
            onDismissRequest = { newBranch = null },
            title = { Text("New branch") },
            text = { OutlinedTextField(value = name, onValueChange = { newBranch = it }, label = { Text("Name") }, singleLine = true) },
            confirmButton = {
                TextButton(enabled = name.isNotBlank(), onClick = {
                    newBranch = null
                    act("Create branch", { api.gitCreateBranch(projectID, name.trim()); "On ${name.trim()}" })
                }) { Text("Create") }
            },
            dismissButton = { TextButton(onClick = { newBranch = null }) { Text("Cancel") } },
        )
    }

    conflicts?.let { list ->
        AlertDialog(
            onDismissRequest = { conflicts = null },
            title = { Text("Merge conflicts") },
            text = {
                Column {
                    Text("${list.size} conflicted path(s):")
                    Text(list.joinToString("\n") { it.path }, fontSize = 12.sp, modifier = Modifier.padding(top = 8.dp))
                    Text(
                        "Resolve these with the Terminal or Files tabs, then stage and commit here.",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(top = 12.dp),
                    )
                }
            },
            confirmButton = { TextButton(onClick = { conflicts = null }) { Text("OK") } },
        )
    }
}

@Composable
private fun GroupHeader(label: String, allLabel: String?, onAll: () -> Unit, secondLabel: String? = null, onSecond: () -> Unit = {}) {
    Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
        Text(label, style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.weight(1f))
        if (secondLabel != null) TextButton(onClick = onSecond) { Text(secondLabel, fontSize = 12.sp, color = MaterialTheme.colorScheme.error) }
        if (allLabel != null) TextButton(onClick = onAll) { Text(allLabel, fontSize = 12.sp) }
    }
}

@Composable
private fun ScmFileRow(row: ScmRow, trailing: String, onToggle: () -> Unit, onOpen: () -> Unit, onDiscard: (() -> Unit)? = null) {
    Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 2.dp), verticalAlignment = Alignment.CenterVertically) {
        Column(
            Modifier
                .weight(1f)
                .clip(RoundedCornerShape(8.dp))
                .background(MaterialTheme.colorScheme.surfaceVariant)
                .padding(8.dp),
        ) {
            Text(row.label, style = MaterialTheme.typography.bodyMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
            Text(
                row.chip,
                fontSize = 11.sp,
                color = MaterialTheme.colorScheme.primary,
                modifier = Modifier.padding(top = 2.dp).clip(RoundedCornerShape(999.dp)).background(MaterialTheme.colorScheme.surface).padding(horizontal = 8.dp, vertical = 1.dp),
            )
        }
        TextButton(onClick = onOpen) { Text("diff", fontSize = 12.sp) }
        if (onDiscard != null) TextButton(onClick = onDiscard) { Text("discard", fontSize = 12.sp, color = MaterialTheme.colorScheme.error) }
        TextButton(onClick = onToggle) { Text(trailing, fontSize = 12.sp) }
    }
}

@Composable
private fun ScmCommitRow(commit: GitCommitInfo) {
    Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 6.dp)) {
        Text(
            commit.sha.take(7),
            fontSize = 12.sp,
            fontFamily = FontFamily.Monospace,
            color = MaterialTheme.colorScheme.primary,
            modifier = Modifier.width(64.dp),
        )
        Column(Modifier.weight(1f)) {
            Text(commit.subject, style = MaterialTheme.typography.bodyMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
            Text(
                listOfNotNull(commit.author.ifBlank { null }, fmt(commit.datetime)).joinToString(" · "),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

/**
 * Full-screen diff overlay (US-SC2, OQ-2): one path's unified diff, monospace,
 * +/- tinted, one shared horizontal scroll across the lane, with a
 * staged/worktree toggle.
 */
@Composable
fun DiffScreen(server: Server, projectID: String, path: String, initialStaged: Boolean, onBack: () -> Unit) {
    val api = remember { Api(server) }
    var staged by remember { mutableStateOf(initialStaged) }
    var diff by remember { mutableStateOf<String?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    val scroll = rememberScrollState()

    LaunchedEffect(path, staged) {
        error = null
        try {
            diff = withContext(Dispatchers.IO) { api.gitDiff(projectID, path, staged) }
        } catch (e: Exception) {
            error = "Diff failed: ${e.message?.take(200)}"
            diff = null
        }
    }

    BackHandler(onBack = onBack)
    Scaffold { pad ->
        Column(Modifier.padding(pad).fillMaxSize()) {
            ScreenHeader(
                title = path,
                onBack = onBack,
                actions = {
                    TextButton(enabled = diff != null, onClick = { staged = !staged }) {
                        Text(if (staged) "Staged" else "Worktree", fontSize = 12.sp)
                    }
                },
            )
            val d = diff
            when {
                error != null -> Text(error.orEmpty(), color = MaterialTheme.colorScheme.error, fontSize = 12.sp, modifier = Modifier.padding(16.dp))
                d == null -> CircularProgressIndicator(Modifier.padding(24.dp))
                d.isBlank() -> Text(
                    if (staged) "No staged diff for this path." else "No unstaged diff for this path.",
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(16.dp),
                )
                else -> {
                    val lines = d.split("\n")
                    LazyColumn(Modifier.fillMaxSize().horizontalScroll(scroll), contentPadding = PaddingValues(horizontal = 8.dp, vertical = 8.dp)) {
                        items(lines.size) { i ->
                            val line = lines[i]
                            val color = when {
                                line.startsWith("+++") || line.startsWith("---") || line.startsWith("diff ") || line.startsWith("index ") -> MaterialTheme.colorScheme.onSurfaceVariant
                                line.startsWith("+") -> diffAddedColor
                                line.startsWith("-") -> diffRemovedColor
                                line.startsWith("@@") -> MaterialTheme.colorScheme.primary
                                else -> MaterialTheme.colorScheme.onSurface
                            }
                            Text(
                                line,
                                fontSize = 12.sp,
                                lineHeight = 16.sp,
                                fontFamily = FontFamily.Monospace,
                                color = color,
                                softWrap = false,
                            )
                        }
                    }
                }
            }
        }
    }
}