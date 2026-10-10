package dev.devstationone.ui

import android.net.Uri
import android.os.Build
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Switch
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
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanOptions
import dev.devstationone.BuildConfig
import dev.devstationone.R
import dev.devstationone.data.DeviceKey
import dev.devstationone.data.Server
import dev.devstationone.data.ServerStore
import dev.devstationone.net.Api
import dev.devstationone.net.ApiException
import dev.devstationone.net.ProjectInfo
import dev.devstationone.ui.kit.MuxButton
import dev.devstationone.ui.kit.MuxCard
import dev.devstationone.ui.kit.MuxIcons
import dev.devstationone.ui.kit.MuxTonalButton
import dev.devstationone.ui.kit.RoundIconButton
import dev.devstationone.ui.kit.ScreenHeader
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.util.UUID

// ---------------------------------------------------------------- servers

@Composable
fun ServerListScreen(store: ServerStore, onSettings: () -> Unit, onOpen: (Server) -> Unit, onAdd: () -> Unit) {
    var servers by remember { mutableStateOf(store.load()) }
    var deleting by remember { mutableStateOf<Server?>(null) }
    Scaffold { pad ->
        Column(Modifier.padding(pad).fillMaxSize()) {
            ScreenHeader(
                title = "Servers",
                actions = { RoundIconButton(MuxIcons.Plus, "Add server", onAdd) },
            )
            if (servers.isEmpty()) {
                Column(
                    Modifier.weight(1f).fillMaxWidth().padding(24.dp),
                    verticalArrangement = Arrangement.Center,
                    horizontalAlignment = Alignment.CenterHorizontally,
                ) {
                    Text("DevStation", style = MaterialTheme.typography.headlineSmall)
                    Text(
                        "No paired servers yet.\n\nOn the server run:\ndevstationone-admin pair --email you@example.com --name \"Pixel\" --url https://your.host\nthen scan the QR code.",
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        textAlign = TextAlign.Center,
                        modifier = Modifier.padding(vertical = 12.dp),
                    )
                    MuxButton("Pair a server", onAdd, Modifier.padding(top = 12.dp))
                }
            } else {
                LazyColumn(Modifier.weight(1f), contentPadding = androidx.compose.foundation.layout.PaddingValues(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    items(servers, key = { it.id }) { s ->
                        MuxCard(onClick = { onOpen(s) }) {
                            Row(Modifier.padding(16.dp), verticalAlignment = Alignment.CenterVertically) {
                                Column(Modifier.weight(1f).padding(horizontal = 4.dp)) {
                                    Text(s.name, style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                    Text(s.url, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                }
                                TextButton(onClick = { deleting = s }) { Text("Remove") }
                            }
                        }
                    }
                }
            }
            TextButton(onClick = onSettings, modifier = Modifier.align(Alignment.CenterHorizontally)) { Text("Settings") }
            Text(
                "DevStation ${BuildConfig.VERSION_NAME}",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.align(Alignment.CenterHorizontally).padding(8.dp),
            )
        }
    }
    deleting?.let { s ->
        AlertDialog(
            onDismissRequest = { deleting = null },
            title = { Text("Remove ${s.name}?") },
            text = { Text("Deletes this phone's key for the server. Also revoke the device in the web UI (Settings → Paired devices) or with DELETE /api/v1/auth/devices.") },
            confirmButton = {
                TextButton(onClick = { store.remove(s.id); servers = store.load(); deleting = null }) { Text("Remove") }
            },
            dismissButton = { TextButton(onClick = { deleting = null }) { Text("Cancel") } },
        )
    }
}

// ---------------------------------------------------------------- projects

@Composable
fun ProjectsScreen(server: Server, onBack: () -> Unit, onOpen: (ProjectInfo) -> Unit) {
    val scope = rememberCoroutineScope()
    var projects by remember { mutableStateOf<List<ProjectInfo>>(emptyList()) }
    var error by remember { mutableStateOf<String?>(null) }
    BackHandler(onBack = onBack)

    LaunchedEffect(server.id) {
        error = null
        try {
            projects = withContext(Dispatchers.IO) { Api(server).projects() }
        } catch (e: Exception) {
            error = "Failed to load projects: ${e.message}"
        }
    }

    Scaffold { pad ->
        Column(Modifier.padding(pad).fillMaxSize()) {
            ScreenHeader(server.name.ifEmpty { "Projects" }, onBack = onBack)
            error?.let { Text(it, color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(16.dp)) }
            LazyColumn(Modifier.weight(1f), contentPadding = androidx.compose.foundation.layout.PaddingValues(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                items(projects, key = { it.id }) { p ->
                    MuxCard(onClick = { onOpen(p) }) {
                        Row(Modifier.padding(16.dp), verticalAlignment = Alignment.CenterVertically) {
                            Column(Modifier.weight(1f).padding(horizontal = 4.dp)) {
                                Text(p.name, style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            }
                        }
                    }
                }
            }
        }
    }
}

@Composable
fun SettingsScreen(
    lockEnabled: Boolean,
    onToggleLock: () -> Unit,
    allowScreenshots: Boolean,
    onAllowScreenshots: (Boolean) -> Unit,
    onBack: () -> Unit,
) {
    BackHandler(onBack = onBack)
    Scaffold { pad ->
        Column(Modifier.padding(pad).fillMaxSize()) {
            ScreenHeader("Settings", onBack = onBack)
            SettingRow("App lock", "Ask for the phone's screen lock when the app opens, whenever the screen turns off, and after a minute away in another app.", lockEnabled) { onToggleLock() }
            SettingRow("Allow screenshots", "Off hides the app from screenshots, screen recording and the recent-apps preview.", allowScreenshots, onAllowScreenshots)
        }
    }
}

@Composable
private fun SettingRow(title: String, note: String, checked: Boolean, onChange: (Boolean) -> Unit) {
    Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 12.dp), verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f).padding(end = 12.dp)) {
            Text(title, style = MaterialTheme.typography.titleMedium)
            Text(note, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        Switch(checked, onChange)
    }
}

@Composable
fun LockedScreen(onUnlock: () -> Unit) {
    Scaffold { pad ->
        Column(
            Modifier.padding(pad).fillMaxSize().padding(24.dp),
            verticalArrangement = Arrangement.Center,
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Text("DevStation is locked", style = MaterialTheme.typography.headlineSmall, modifier = Modifier.padding(vertical = 16.dp))
            MuxButton("Unlock", onUnlock)
        }
    }
}

// ---------------------------------------------------------------- pairing

@Composable
fun PairScreen(store: ServerStore, initialUrl: String, initialCode: String, onDone: () -> Unit, onCancel: () -> Unit) {
    val scope = rememberCoroutineScope()
    var url by remember { mutableStateOf(initialUrl) }
    var code by remember { mutableStateOf(initialCode) }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    var confirm by remember { mutableStateOf(false) }
    BackHandler(onBack = onCancel)

    val scan = rememberLauncherForActivityResult(ScanContract()) { res ->
        val uri = res.contents?.let { runCatching { Uri.parse(it) }.getOrNull() }
        if (uri != null && uri.scheme == "devstation" && uri.host == "pair") {
            uri.getQueryParameter("url")?.let { url = it }
            uri.getQueryParameter("code")?.let { code = it }
        } else if (res.contents != null) error = "Not a pairing QR code"
    }

    fun pair() {
        confirm = false
        val cleanUrl = url.trim().trimEnd('/')
        if (!cleanUrl.startsWith("https://")) { error = "URL must start with https://"; return }
        busy = true
        error = null
        scope.launch {
            val id = UUID.randomUUID().toString()
            try {
                val deviceId = withContext(Dispatchers.IO) {
                    val pub = DeviceKey.create(id) // private key stays in the Keystore
                    Api.pair(cleanUrl, code.trim(), pub)
                }
                val host = Uri.parse(cleanUrl).host ?: cleanUrl
                store.add(Server(id, host, cleanUrl, deviceId))
                onDone()
            } catch (e: Exception) {
                DeviceKey.delete(id)
                error = when {
                    e is ApiException && e.code == 400 -> "Invalid, expired, or already-used code. Run devstationone-admin pair again."
                    e is ApiException && e.code == 429 -> "Too many attempts. Wait 15 minutes."
                    else -> e.message?.take(200) ?: e.javaClass.simpleName
                }
            } finally {
                busy = false
            }
        }
    }

    Scaffold { pad ->
        Column(Modifier.padding(pad).fillMaxSize()) {
            ScreenHeader("Pair server", onBack = onCancel)
            Column(Modifier.padding(16.dp).fillMaxSize(), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                MuxTonalButton("Scan QR code", {
                    scan.launch(ScanOptions().setDesiredBarcodeFormats(ScanOptions.QR_CODE).setPrompt("Scan the devstationone-admin pair QR").setBeepEnabled(false))
                })
                OutlinedTextField(url, { url = it }, label = { Text("Server URL") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                OutlinedTextField(code, { code = it }, label = { Text("Pairing code") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
                // A link can pre-fill any server, so ask before pairing with one nobody typed in.
                MuxButton("Pair", { if (initialUrl.isNotEmpty() && url == initialUrl) confirm = true else pair() }, enabled = !busy && url.isNotBlank() && code.isNotBlank())
                if (busy) CircularProgressIndicator(Modifier.align(Alignment.CenterHorizontally))
                Text(
                    "A key pair is generated on this phone. Only the public key is sent to the server; " +
                        "the private key never leaves the Android Keystore.",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
    if (confirm) {
        val host = Uri.parse(url.trim()).host ?: url
        AlertDialog(
            onDismissRequest = { confirm = false },
            title = { Text("Pair with $host?") },
            text = { Text("This link filled in the server. Only continue if you started this pairing yourself.") },
            confirmButton = { TextButton(onClick = { pair() }) { Text("Pair") } },
            dismissButton = { TextButton(onClick = { confirm = false }) { Text("Cancel") } },
        )
    }
}