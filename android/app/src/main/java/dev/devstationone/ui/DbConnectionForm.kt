package dev.devstationone.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
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
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import dev.devstationone.net.Api
import dev.devstationone.net.DbConnForm
import dev.devstationone.net.dbDelete
import dev.devstationone.net.dbSave
import dev.devstationone.net.dbTest
import dev.devstationone.ui.kit.MuxButton
import dev.devstationone.ui.kit.MuxDropdown
import dev.devstationone.ui.kit.ScreenHeader
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * Add / edit a connection (design §5, slice 6). Engine is fixed once saved. The password lives only
 * in this composable's plain `remember` (never saveable, never logged) and leaves with the screen;
 * on edit a blank password keeps the stored one. Test runs against the saved record, so it is
 * offered only when editing. `extra_params` stays web-only (D4/D9).
 */
@Composable
internal fun ConnectionForm(api: Api, projectID: String, r: DbRoute.Form, onBack: () -> Unit, onChanged: () -> Unit) {
    val scope = rememberCoroutineScope()
    val c = r.conn
    var engine by remember { mutableStateOf(c?.engine ?: "postgresql") }
    var name by remember { mutableStateOf(c?.name ?: "") }
    var host by remember { mutableStateOf(c?.host ?: "") }
    var port by remember { mutableStateOf(c?.let { if (it.port > 0) it.port.toString() else "" } ?: defaultPort("postgresql").toString()) }
    var username by remember { mutableStateOf(c?.username ?: "") }
    var database by remember { mutableStateOf(c?.database ?: "") }
    var sslMode by remember { mutableStateOf(c?.ssl_mode ?: "") }
    var instance by remember { mutableStateOf(c?.instance_name ?: "") }
    var searchPath by remember { mutableStateOf(c?.search_path ?: "") }
    var password by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    var notice by remember { mutableStateOf<String?>(null) }
    var confirmDelete by remember { mutableStateOf(false) }
    val sqlite = engine == "sqlite"

    fun pickEngine(e: String) {
        if (port.isBlank() || port == defaultPort(engine).toString()) port = if (defaultPort(e) > 0) defaultPort(e).toString() else ""
        engine = e
    }

    /** Runs one request off the main thread; failures show the server text verbatim. */
    fun act(block: suspend () -> Unit) {
        if (busy) return
        busy = true
        error = null
        notice = null
        scope.launch {
            try {
                block()
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                error = dbErrorText(e.message)
            }
            busy = false
        }
    }

    fun save() {
        validateConnection(engine, name, host, port, database)?.let { error = it; return }
        val form = DbConnForm(
            name.trim(), engine, if (sqlite) "" else host.trim(), if (sqlite) 0 else port.trim().toIntOrNull() ?: 0,
            if (sqlite) "" else username.trim(), database.trim(), if (sqlite) "" else sslMode.trim(),
            if (engine == "mssql") instance.trim() else "", if (engine == "postgresql") searchPath.trim() else "",
        )
        act {
            withContext(Dispatchers.IO) { api.dbSave(projectID, c?.id, form, if (sqlite) "" else password) }
            password = ""
            onChanged()
        }
    }

    Column(Modifier.fillMaxSize()) {
        ScreenHeader(if (c == null) "New connection" else "Edit connection", onBack = onBack)
        Column(Modifier.weight(1f).verticalScroll(rememberScrollState()).padding(horizontal = 16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            MuxDropdown("Engine", engine, ENGINE_OPTIONS, { pickEngine(it) }, enabled = c == null)
            Field("Name", name) { name = it }
            if (!sqlite) {
                Field("Host", host, KeyboardType.Uri) { host = it }
                Field("Port", port, KeyboardType.Number) { port = it }
                Field("Username", username) { username = it }
                OutlinedTextField(
                    value = password, onValueChange = { password = it }, modifier = Modifier.fillMaxWidth(), singleLine = true,
                    label = { Text(if (c?.has_secret == true) "Password (blank keeps the stored one)" else "Password") },
                    visualTransformation = PasswordVisualTransformation(),
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, autoCorrectEnabled = false),
                )
            }
            Field(if (sqlite) "File path" else "Database", database, KeyboardType.Uri) { database = it }
            if (!sqlite) Field("SSL mode (e.g. disable, require)", sslMode) { sslMode = it }
            if (engine == "mssql") Field("Instance name (optional)", instance) { instance = it }
            if (engine == "postgresql") Field("Search path (optional)", searchPath) { searchPath = it }
            error?.let { Text(it, color = MaterialTheme.colorScheme.error, fontSize = 13.sp) }
            notice?.let { Text(it, color = MaterialTheme.colorScheme.primary, fontSize = 13.sp) }
            MuxButton(if (busy) "Working…" else "Save", onClick = { save() }, enabled = !busy)
            if (c != null) {
                Row {
                    TextButton(enabled = !busy, onClick = {
                        act {
                            val res = withContext(Dispatchers.IO) { api.dbTest(projectID, c.id) }
                            if (res.ok) notice = "Connection OK (saved settings)" else error = dbErrorText(res.error.ifEmpty { "Test failed" })
                        }
                    }) { Text("Test saved") }
                    TextButton(enabled = !busy, onClick = { confirmDelete = true }) { Text("Delete", color = MaterialTheme.colorScheme.error) }
                }
            }
        }
    }

    if (confirmDelete && c != null) {
        AlertDialog(
            onDismissRequest = { confirmDelete = false },
            title = { Text("Delete connection?") },
            text = { Text("Removes \"${c.name}\" and its stored credentials from this project. The database itself is not touched.") },
            confirmButton = {
                TextButton(onClick = {
                    confirmDelete = false
                    act {
                        withContext(Dispatchers.IO) { api.dbDelete(projectID, c.id) }
                        onChanged()
                    }
                }) { Text("Delete", color = MaterialTheme.colorScheme.error) }
            },
            dismissButton = { TextButton(onClick = { confirmDelete = false }) { Text("Cancel") } },
        )
    }
}

@Composable
private fun Field(label: String, value: String, keyboard: KeyboardType = KeyboardType.Text, onChange: (String) -> Unit) {
    OutlinedTextField(
        value = value, onValueChange = onChange, modifier = Modifier.fillMaxWidth(), singleLine = true,
        label = { Text(label) },
        keyboardOptions = KeyboardOptions(keyboardType = keyboard, autoCorrectEnabled = false),
    )
}
