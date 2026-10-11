package dev.devstationone.ui

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
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
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import dev.devstationone.net.Api
import dev.devstationone.net.DbBackup
import dev.devstationone.net.DbSchedule
import dev.devstationone.net.dbBackups
import dev.devstationone.net.dbCreateBackup
import dev.devstationone.net.dbDeleteBackup
import dev.devstationone.net.dbDeleteSchedule
import dev.devstationone.net.dbDownloadBackup
import dev.devstationone.net.dbPutSchedule
import dev.devstationone.net.dbRestore
import dev.devstationone.net.dbSchedule
import dev.devstationone.ui.kit.MuxChip
import dev.devstationone.ui.kit.ScreenHeader
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

private val CADENCES = listOf("hourly", "daily", "weekly")

private class BackupsState(val backups: List<DbBackup>, val schedule: DbSchedule?)

/**
 * Backups of one connection (design slice 8): list (polled every 3 s while one is running), create,
 * schedule, download to a user-picked file, restore, delete. Restore blocks on the server for up to
 * 30 minutes, so its dialog stays up until the runtime answers and asks to keep the app open.
 */
@Composable
internal fun BackupsView(api: Api, projectID: String, conn: dev.devstationone.net.DbConnection, onBack: () -> Unit) {
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    val acting = remember { Acting(scope) }
    var rev by remember { mutableIntStateOf(0) }
    var state by remember { mutableStateOf<BackupsState?>(null) }
    var loadError by remember { mutableStateOf<String?>(null) }
    var notice by remember { mutableStateOf<String?>(null) }
    var scheduling by remember { mutableStateOf(false) }
    var deleting by remember { mutableStateOf<DbBackup?>(null) }
    var restoring by remember { mutableStateOf<DbBackup?>(null) }
    var downloading by remember { mutableStateOf<DbBackup?>(null) }

    LaunchedEffect(rev) {
        try {
            val s = withContext(Dispatchers.IO) { BackupsState(api.dbBackups(projectID, conn.id), api.dbSchedule(projectID, conn.id)) }
            state = s
            loadError = null
            if (anyRunning(s.backups)) {
                delay(3000)
                rev++ // restarts this effect: poll again
            }
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            loadError = dbErrorText(e.message)
        }
    }

    val save = rememberLauncherForActivityResult(ActivityResultContracts.CreateDocument("application/octet-stream")) { uri ->
        val b = downloading
        downloading = null
        if (uri == null || b == null) return@rememberLauncherForActivityResult
        acting.run({
            ctx.contentResolver.openOutputStream(uri)!!.use { api.dbDownloadBackup(projectID, conn.id, b.id, it) }
        }, { notice = "Saved ${b.file_name.substringAfterLast('/')}" })
    }

    Column(Modifier.fillMaxSize()) {
        ScreenHeader("Backups · ${conn.name}", onBack = onBack, actions = {
            TextButton(enabled = !acting.busy, onClick = {
                notice = null
                acting.run({ api.dbCreateBackup(projectID, conn.id) }, { rev++ })
            }) { Text("+ Backup") }
        })
        val s = state
        if (s == null) {
            if (loadError == null) CircularProgressIndicator(Modifier.padding(24.dp))
        } else {
            val sch = s.schedule
            Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp), horizontalArrangement = Arrangement.SpaceBetween) {
                Text(
                    if (sch == null) "No schedule" else "${sch.cadence}, keep ${sch.retention} · next ${whenText(sch.next_run_at)}",
                    fontSize = 13.sp, modifier = Modifier.weight(1f).padding(top = 12.dp),
                )
                TextButton(onClick = { acting.error = null; scheduling = true }) { Text(if (sch == null) "Schedule" else "Edit") }
            }
            if (sch != null && sch.last_result == "failed") {
                Text("Last scheduled run failed: ${sch.last_error.take(200)}", color = MaterialTheme.colorScheme.error, fontSize = 12.sp, modifier = Modifier.padding(horizontal = 16.dp))
            }
        }
        loadError?.let {
            Column(Modifier.padding(16.dp)) {
                Text(it, color = MaterialTheme.colorScheme.error, fontSize = 13.sp)
                TextButton(onClick = { loadError = null; rev++ }) { Text("Retry") }
            }
        }
        if (acting.busy) Text("Working…", fontSize = 13.sp, modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp))
        acting.error?.takeIf { !scheduling && restoring == null && deleting == null }?.let {
            Text(it, color = MaterialTheme.colorScheme.error, fontSize = 13.sp, modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp))
        }
        notice?.let { Text(it, color = MaterialTheme.colorScheme.primary, fontSize = 13.sp, modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp)) }
        state?.let { s ->
            if (s.backups.isEmpty()) Text("No backups yet.", color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(16.dp))
            LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(horizontal = 16.dp)) {
                items(s.backups, key = { it.id }) { b ->
                    Column(Modifier.fillMaxWidth().padding(vertical = 8.dp)) {
                        Text(whenText(b.created_at), fontSize = 15.sp, fontFamily = FontFamily.Monospace)
                        Text(
                            listOfNotNull(b.trigger, b.state, if (b.state == "done") fmtSize(b.size_bytes) else null).joinToString(" · "),
                            fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        if (b.state == "failed" && b.error.isNotEmpty()) Text(b.error.take(200), fontSize = 12.sp, color = MaterialTheme.colorScheme.error)
                        Row {
                            if (b.state == "done") {
                                TextButton(enabled = !acting.busy, onClick = { notice = null; downloading = b; save.launch(b.file_name.substringAfterLast('/')) }) { Text("Download") }
                                TextButton(enabled = !acting.busy, onClick = { acting.error = null; restoring = b }) { Text("Restore") }
                            }
                            if (b.state != "running") {
                                TextButton(enabled = !acting.busy, onClick = { acting.error = null; deleting = b }) { Text("Delete", color = MaterialTheme.colorScheme.error) }
                            }
                        }
                    }
                }
            }
        }
    }

    if (scheduling) {
        val cur = state?.schedule
        var cadence by remember { mutableStateOf(cur?.cadence ?: "daily") }
        var retention by remember { mutableStateOf((cur?.retention ?: 7).toString()) }
        var invalid by remember { mutableStateOf<String?>(null) }
        AlertDialog(
            onDismissRequest = { if (!acting.busy) scheduling = false },
            title = { Text("Backup schedule") },
            text = {
                Column {
                    Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) { CADENCES.forEach { c -> MuxChip(c, selected = cadence == c, onClick = { cadence = c }) } }
                    OutlinedTextField(
                        retention, { retention = it }, label = { Text("Keep last (1–100)") }, singleLine = true,
                        modifier = Modifier.fillMaxWidth().padding(top = 8.dp), keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                    )
                    (invalid ?: acting.error)?.let { Text(it, color = MaterialTheme.colorScheme.error, fontSize = 13.sp, modifier = Modifier.padding(top = 8.dp)) }
                }
            },
            confirmButton = {
                TextButton(enabled = !acting.busy, onClick = {
                    val keep = parseRetention(retention)
                    invalid = if (keep == null) "Keep must be 1–100" else null
                    if (keep != null) acting.run({ api.dbPutSchedule(projectID, conn.id, cadence, keep) }, { scheduling = false; rev++ })
                }) { Text("Save") }
            },
            dismissButton = {
                Row {
                    if (cur != null) TextButton(enabled = !acting.busy, onClick = {
                        acting.run({ api.dbDeleteSchedule(projectID, conn.id) }, { scheduling = false; rev++ })
                    }) { Text("Remove", color = MaterialTheme.colorScheme.error) }
                    TextButton(enabled = !acting.busy, onClick = { scheduling = false }) { Text("Cancel") }
                }
            },
        )
    }

    deleting?.let { b ->
        AlertDialog(
            onDismissRequest = { if (!acting.busy) deleting = null },
            title = { Text("Delete backup?") },
            text = {
                Column {
                    Text("Deletes the ${whenText(b.created_at)} backup and its file on the runtime. This can't be undone.")
                    acting.error?.let { Text(it, color = MaterialTheme.colorScheme.error, fontSize = 13.sp, modifier = Modifier.padding(top = 8.dp)) }
                }
            },
            confirmButton = {
                TextButton(enabled = !acting.busy, onClick = { acting.run({ api.dbDeleteBackup(projectID, conn.id, b.id) }, { deleting = null; rev++ }) }) {
                    Text(if (acting.busy) "Deleting…" else "Delete", color = MaterialTheme.colorScheme.error)
                }
            },
            dismissButton = { TextButton(enabled = !acting.busy, onClick = { deleting = null }) { Text("Cancel") } },
        )
    }

    restoring?.let { b ->
        var target by remember { mutableStateOf(conn.database) }
        var typed by remember { mutableStateOf("") }
        AlertDialog(
            onDismissRequest = { if (!acting.busy) restoring = null },
            title = { Text("Restore backup?") },
            text = {
                Column {
                    Text("Overwrites the data in the target below with the ${whenText(b.created_at)} backup. This can't be undone.", fontSize = 14.sp)
                    OutlinedTextField(
                        target, { target = it; typed = "" }, label = { Text(if (conn.engine == "sqlite") "Target file" else "Target database") },
                        singleLine = true, enabled = !acting.busy, modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
                    )
                    OutlinedTextField(
                        typed, { typed = it }, label = { Text("Type the target name to confirm") },
                        singleLine = true, enabled = !acting.busy, modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
                    )
                    if (acting.busy) Text("Restoring — this can take minutes. Keep the app open.", fontSize = 13.sp, modifier = Modifier.padding(top = 8.dp))
                    acting.error?.let { Text(it, color = MaterialTheme.colorScheme.error, fontSize = 13.sp, modifier = Modifier.padding(top = 8.dp)) }
                }
            },
            confirmButton = {
                TextButton(enabled = !acting.busy && restoreConfirmed(typed, target), onClick = {
                    val t = target.trim()
                    acting.run({ api.dbRestore(projectID, conn.id, b.id, t) }, { restoring = null; notice = "Restored into $t"; rev++ })
                }) { Text(if (acting.busy) "Restoring…" else "Restore", color = MaterialTheme.colorScheme.error) }
            },
            dismissButton = { TextButton(enabled = !acting.busy, onClick = { restoring = null }) { Text("Cancel") } },
        )
    }
}
