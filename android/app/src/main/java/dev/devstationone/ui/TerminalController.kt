package dev.devstationone.ui

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import dev.devstationone.data.Server
import dev.devstationone.data.TabColorStore
import dev.devstationone.net.Api
import dev.devstationone.net.ConnState
import dev.devstationone.net.SessionInfo
import dev.devstationone.net.TerminalConnection
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** Session statuses that mean the PTY is gone — not worth a tab. */
val TERMINAL_STATUSES = setOf("exited", "closed", "failed")

/** Tabs for one project. Each tab is a PTY session on that project's runtime. */
class TerminalController(val server: Server, val projectID: String, private val scope: CoroutineScope, private val colorStore: TabColorStore) {
    val api = Api(server)
    val tabs = mutableStateListOf<SessionInfo>()
    var selected by mutableStateOf<String?>(null)
    var error by mutableStateOf<String?>(null)
    val states = mutableStateMapOf<String, ConnState>()
    val tabColors = mutableStateMapOf<String, Int>() // ARGB; chosen per device

    private val conns = HashMap<String, TerminalConnection>()
    private val encs = HashMap<String, InputEncoder>()

    fun conn(id: String): TerminalConnection = conns.getOrPut(id) {
        TerminalConnection(server, id).also { c -> c.onState = { s -> states[id] = s } }
    }

    fun enc(id: String): InputEncoder = encs.getOrPut(id) { InputEncoder(conn(id)) }

    /** Open a tab for every live session of the project. */
    fun load() {
        scope.launch {
            try {
                val list = withContext(Dispatchers.IO) { api.sessions(projectID) }
                list.filter { it.status !in TERMINAL_STATUSES }.forEach {
                    if (it.id !in tabs.map { t -> t.id }) {
                        tabs.add(it)
                        colorStore.get(server.id, it.id).takeIf { c -> c != 0 }?.let { c -> tabColors[it.id] = c }
                    }
                }
                // Sessions that reached a terminal state while we weren't looking lose their tabs.
                tabs.removeAll { t -> t.status in TERMINAL_STATUSES || list.none { it.id == t.id && it.status !in TERMINAL_STATUSES } }
                error = null
            } catch (e: Exception) {
                error = e.message ?: e.javaClass.simpleName
            }
            if (tabs.isEmpty()) error ?: run { withContext(Dispatchers.IO) { newTab("main") } }
            if (selected == null || selected !in tabs.map { it.id }) selected = tabs.firstOrNull()?.id
        }
    }

    /** Create a named session (its PTY starts on the project runtime) and open its tab. */
    fun newTab(name: String) {
        if (name.isBlank()) return
        scope.launch {
            try {
                val s = withContext(Dispatchers.IO) { api.createSession(projectID, name.trim(), defaultCols, defaultRows) }
                if (s.id !in tabs.map { t -> t.id }) tabs.add(s)
                selected = s.id
                error = null
            } catch (e: Exception) {
                error = "Create failed: ${e.message}"
            }
        }
    }

    /** Chip color for a tab, client-side only; 0 removes it. */
    fun setColor(id: String, argb: Int) {
        if (argb == 0) tabColors.remove(id) else tabColors[id] = argb
        colorStore.set(server.id, id, argb)
    }

    /** Detach (session keeps running) or kill it on the runtime. */
    fun closeTab(id: String, kill: Boolean) {
        conns.remove(id)?.close()
        encs.remove(id)
        states.remove(id)
        val idx = tabs.indexOfFirst { it.id == id }
        if (idx >= 0) tabs.removeAt(idx)
        if (selected == id) selected = tabs.getOrNull(minOf(idx, tabs.size - 1))?.id
        if (!kill) {
            scope.launch { withContext(Dispatchers.IO) { runCatching { api.detach(id) } } }
        } else {
            scope.launch {
                try {
                    withContext(Dispatchers.IO) { api.kill(id) }
                } catch (e: Exception) {
                    error = "Kill failed: ${e.message}"
                }
            }
        }
    }

    fun kick() = conns.values.toList().forEach { it.kick() }

    /** Rename the session label; the WS stays attached through the rename. */
    fun renameTab(id: String, to: String) {
        if (to.isBlank()) return
        scope.launch {
            try {
                withContext(Dispatchers.IO) { api.rename(id, to.trim()) }
            } catch (e: Exception) {
                error = "Rename failed: ${e.message}"
                return@launch
            }
            val i = tabs.indexOfFirst { it.id == id }
            if (i >= 0) tabs[i] = tabs[i].copy(name = to.trim())
        }
    }

    fun dispose() {
        conns.values.toList().forEach { it.close() }
        conns.clear()
    }

    companion object {
        const val defaultCols = 80
        const val defaultRows = 24
    }
}