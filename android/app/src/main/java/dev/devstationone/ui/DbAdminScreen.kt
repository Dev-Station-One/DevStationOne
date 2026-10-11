package dev.devstationone.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Checkbox
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import dev.devstationone.net.Api
import dev.devstationone.net.DbConnection
import dev.devstationone.net.DbGrant
import dev.devstationone.net.dbChangeGrant
import dev.devstationone.net.dbCreatePrincipal
import dev.devstationone.net.dbDropPrincipal
import dev.devstationone.net.dbGrants
import dev.devstationone.net.dbPrincipals
import dev.devstationone.net.dbSetPrincipalPassword
import dev.devstationone.ui.kit.MuxChip
import dev.devstationone.ui.kit.ScreenHeader
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** Busy/error wrapper for one admin screen; failures show the server text verbatim (design §6). */
private class Acting(private val scope: kotlinx.coroutines.CoroutineScope) {
    var busy by mutableStateOf(false)
    var error by mutableStateOf<String?>(null)

    fun run(block: () -> Unit, onDone: () -> Unit) {
        if (busy) return
        busy = true
        error = null
        scope.launch {
            try {
                withContext(Dispatchers.IO) { block() }
                onDone()
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                error = dbErrorText(e.message)
            }
            busy = false
        }
    }
}

private sealed interface PDialog {
    data object Create : PDialog
    data class Password(val name: String) : PDialog
    data class Drop(val name: String) : PDialog
}

@Composable
private fun DialogError(msg: String?) {
    msg?.let { Text(it, color = MaterialTheme.colorScheme.error, fontSize = 13.sp, modifier = Modifier.padding(top = 8.dp)) }
}

/**
 * Users/roles of a connection (design §5, slice 7): list, create, set password, drop. SQLite shows an
 * explanation instead of calling the API. Passwords live in plain `remember` and are never logged.
 */
@Composable
internal fun PrincipalsView(api: Api, projectID: String, conn: DbConnection, onBack: () -> Unit, onGrants: (String) -> Unit) {
    val scope = rememberCoroutineScope()
    val acting = remember { Acting(scope) }
    var rev by remember { mutableIntStateOf(0) }
    var dialog by remember { mutableStateOf<PDialog?>(null) }
    Column(Modifier.fillMaxSize()) {
        ScreenHeader(
            "Users · ${conn.name}", onBack = onBack,
            actions = { if (userManagementSupported(conn.engine)) TextButton(onClick = { acting.error = null; dialog = PDialog.Create }) { Text("+ New") } },
        )
        if (!userManagementSupported(conn.engine)) {
            Text("SQLite has no server-side users or roles.", color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(16.dp))
        } else {
            Loaded(conn.id to rev, { api.dbPrincipals(projectID, conn.id) }) { list ->
                if (list.isEmpty()) Text("No users.", color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(16.dp))
                LazyColumn(Modifier.fillMaxSize(), contentPadding = androidx.compose.foundation.layout.PaddingValues(horizontal = 16.dp)) {
                    items(list, key = { it.name }) { p ->
                        Column(Modifier.fillMaxWidth().padding(vertical = 8.dp)) {
                            Text(p.name, fontSize = 15.sp, fontFamily = FontFamily.Monospace)
                            Text(
                                listOfNotNull(if (p.can_login) "can login" else "no login", if (p.superuser) "superuser" else null).joinToString(" · "),
                                fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                            Row {
                                TextButton(onClick = { onGrants(p.name) }) { Text("Grants") }
                                TextButton(onClick = { acting.error = null; dialog = PDialog.Password(p.name) }) { Text("Password") }
                                TextButton(onClick = { acting.error = null; dialog = PDialog.Drop(p.name) }) { Text("Drop", color = MaterialTheme.colorScheme.error) }
                            }
                        }
                    }
                }
            }
        }
    }

    fun done() { dialog = null; rev++ }
    when (val d = dialog) {
        PDialog.Create -> {
            var name by remember { mutableStateOf("") }
            var password by remember { mutableStateOf("") }
            var canLogin by remember { mutableStateOf(true) }
            var superuser by remember { mutableStateOf(false) }
            var invalid by remember { mutableStateOf<String?>(null) }
            AlertDialog(
                onDismissRequest = { if (!acting.busy) dialog = null },
                title = { Text("New user") },
                text = {
                    Column {
                        OutlinedTextField(name, { name = it }, label = { Text("Name") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                        OutlinedTextField(
                            password, { password = it }, label = { Text("Password") }, singleLine = true, modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
                            visualTransformation = PasswordVisualTransformation(),
                            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, autoCorrectEnabled = false),
                        )
                        Row { Checkbox(canLogin, { canLogin = it }); Text("Can login", modifier = Modifier.padding(top = 12.dp)) }
                        Row { Checkbox(superuser, { superuser = it }); Text("Superuser", modifier = Modifier.padding(top = 12.dp)) }
                        DialogError(invalid ?: acting.error)
                    }
                },
                confirmButton = {
                    TextButton(enabled = !acting.busy, onClick = {
                        invalid = validatePrincipal(name, password)
                        if (invalid == null) acting.run({ api.dbCreatePrincipal(projectID, conn.id, name.trim(), password, canLogin, superuser) }, ::done)
                    }) { Text(if (acting.busy) "Creating…" else "Create") }
                },
                dismissButton = { TextButton(enabled = !acting.busy, onClick = { dialog = null }) { Text("Cancel") } },
            )
        }
        is PDialog.Password -> {
            var password by remember { mutableStateOf("") }
            AlertDialog(
                onDismissRequest = { if (!acting.busy) dialog = null },
                title = { Text("New password for ${d.name}") },
                text = {
                    Column {
                        OutlinedTextField(
                            password, { password = it }, label = { Text("Password") }, singleLine = true, modifier = Modifier.fillMaxWidth(),
                            visualTransformation = PasswordVisualTransformation(),
                            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, autoCorrectEnabled = false),
                        )
                        DialogError(acting.error)
                    }
                },
                confirmButton = {
                    TextButton(enabled = !acting.busy && password.isNotEmpty(), onClick = {
                        acting.run({ api.dbSetPrincipalPassword(projectID, conn.id, d.name, password) }, ::done)
                    }) { Text(if (acting.busy) "Saving…" else "Set password") }
                },
                dismissButton = { TextButton(enabled = !acting.busy, onClick = { dialog = null }) { Text("Cancel") } },
            )
        }
        is PDialog.Drop -> AlertDialog(
            onDismissRequest = { if (!acting.busy) dialog = null },
            title = { Text("Drop user?") },
            text = {
                Column {
                    Text("Drops \"${d.name}\" from ${conn.name}. This can't be undone.")
                    DialogError(acting.error)
                }
            },
            confirmButton = {
                TextButton(enabled = !acting.busy, onClick = { acting.run({ api.dbDropPrincipal(projectID, conn.id, d.name) }, ::done) }) {
                    Text(if (acting.busy) "Dropping…" else "Drop", color = MaterialTheme.colorScheme.error)
                }
            },
            dismissButton = { TextButton(enabled = !acting.busy, onClick = { dialog = null }) { Text("Cancel") } },
        )
        null -> {}
    }
}

private val QUICK_PRIVILEGES = listOf("SELECT", "INSERT", "UPDATE", "DELETE", "ALL")

/** One user's privileges: grant and revoke. MySQL's raw grant statements are shown read-only. */
@Composable
internal fun GrantsView(api: Api, projectID: String, r: DbRoute.Grants, onBack: () -> Unit) {
    val scope = rememberCoroutineScope()
    val acting = remember { Acting(scope) }
    var rev by remember { mutableIntStateOf(0) }
    var granting by remember { mutableStateOf(false) }
    var revoking by remember { mutableStateOf<DbGrant?>(null) }

    Column(Modifier.fillMaxSize()) {
        ScreenHeader("Grants · ${r.principal}", onBack = onBack, actions = { TextButton(onClick = { acting.error = null; granting = true }) { Text("+ Grant") } })
        Loaded(r to rev, { api.dbGrants(projectID, r.conn.id, r.principal) }) { list ->
            if (list.isEmpty()) Text("No grants.", color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(16.dp))
            LazyColumn(Modifier.fillMaxSize(), contentPadding = androidx.compose.foundation.layout.PaddingValues(horizontal = 16.dp)) {
                items(list) { g ->
                    Row(Modifier.fillMaxWidth().padding(vertical = 6.dp), horizontalArrangement = Arrangement.SpaceBetween) {
                        if (g.raw.isNotEmpty()) {
                            Text(g.raw, fontFamily = FontFamily.Monospace, fontSize = 12.sp)
                        } else {
                            Column(Modifier.weight(1f)) {
                                Text(g.privilege, fontSize = 14.sp)
                                Text(grantTarget(g), fontSize = 12.sp, fontFamily = FontFamily.Monospace, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            }
                            TextButton(onClick = { acting.error = null; revoking = g }) { Text("Revoke", color = MaterialTheme.colorScheme.error) }
                        }
                    }
                }
            }
        }
    }

    fun done() { granting = false; revoking = null; rev++ }
    if (granting) {
        var privilege by remember { mutableStateOf("SELECT") }
        var schema by remember { mutableStateOf("") }
        var table by remember { mutableStateOf("") }
        var invalid by remember { mutableStateOf<String?>(null) }
        AlertDialog(
            onDismissRequest = { if (!acting.busy) granting = false },
            title = { Text("Grant to ${r.principal}") },
            text = {
                Column {
                    Row(horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                        QUICK_PRIVILEGES.forEach { p -> MuxChip(p, selected = privilege == p, onClick = { privilege = p }) }
                    }
                    OutlinedTextField(privilege, { privilege = it }, label = { Text("Privilege") }, singleLine = true, modifier = Modifier.fillMaxWidth().padding(top = 8.dp))
                    OutlinedTextField(schema, { schema = it }, label = { Text("Schema (optional)") }, singleLine = true, modifier = Modifier.fillMaxWidth().padding(top = 8.dp))
                    OutlinedTextField(table, { table = it }, label = { Text("Table") }, singleLine = true, modifier = Modifier.fillMaxWidth().padding(top = 8.dp))
                    DialogError(invalid ?: acting.error)
                }
            },
            confirmButton = {
                TextButton(enabled = !acting.busy, onClick = {
                    invalid = validateGrant(privilege, table)
                    if (invalid == null) {
                        val g = DbGrant(privilege.trim(), schema.trim(), table.trim())
                        acting.run({ api.dbChangeGrant(projectID, r.conn.id, r.principal, g, revoke = false) }, ::done)
                    }
                }) { Text(if (acting.busy) "Granting…" else "Grant") }
            },
            dismissButton = { TextButton(enabled = !acting.busy, onClick = { granting = false }) { Text("Cancel") } },
        )
    }
    revoking?.let { g ->
        AlertDialog(
            onDismissRequest = { if (!acting.busy) revoking = null },
            title = { Text("Revoke privilege?") },
            text = {
                Column {
                    Text("Revokes ${g.privilege} on ${grantTarget(g)} from ${r.principal}.")
                    DialogError(acting.error)
                }
            },
            confirmButton = {
                TextButton(enabled = !acting.busy, onClick = { acting.run({ api.dbChangeGrant(projectID, r.conn.id, r.principal, g, revoke = true) }, ::done) }) {
                    Text(if (acting.busy) "Revoking…" else "Revoke", color = MaterialTheme.colorScheme.error)
                }
            },
            dismissButton = { TextButton(enabled = !acting.busy, onClick = { revoking = null }) { Text("Cancel") } },
        )
    }
}
