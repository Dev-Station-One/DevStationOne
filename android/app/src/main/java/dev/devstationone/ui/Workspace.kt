package dev.devstationone.ui

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Tab
import androidx.compose.material3.TabRow
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import dev.devstationone.data.Server
import dev.devstationone.data.ShortcutStore
import dev.devstationone.data.TabColorStore
import dev.devstationone.ui.kit.ScreenHeader
import dev.devstationone.ui.kit.StatusDot
import dev.devstationone.ui.kit.stateColor

/**
 * The project workspace: a top tab bar over the project's surfaces
 * (docs/DESIGN_ANDROID_PARITY.md §3). Round 1 has "Terminal" and "Journal";
 * later rounds append "Files" and the other web tabs.
 *
 * Neither tab's content is ever left the composition — the hidden one
 * collapses to zero size, so terminal WebViews/PTY streams keep running and
 * the Journal list keeps its state while a note is open. Notes and the code
 * editor (later rounds) overlay this screen instead of pushing it out, for
 * the same reason.
 */
@Composable
fun WorkspaceScreen(
    server: Server,
    projectID: String,
    projectName: String,
    onShortcuts: () -> Unit,
    onBack: () -> Unit,
) {
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    val ctrl = remember(projectID) { TerminalController(server, projectID, scope, TabColorStore(ctx)) }
    val shortcuts = remember { ShortcutStore(ctx).load() }
    var tab by remember { mutableIntStateOf(0) }
    var noteID by remember { mutableStateOf<String?>(null) }

    DisposableEffect(ctrl) {
        ctrl.load()
        val cm = ctx.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
        val cb = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) = ctrl.kick()
        }
        cm.registerDefaultNetworkCallback(cb)
        onDispose {
            runCatching { cm.unregisterNetworkCallback(cb) }
            ctrl.dispose()
        }
    }
    BackHandler(onBack = onBack)

    Scaffold { pad ->
        Box(Modifier.padding(pad).consumeWindowInsets(pad).imePadding().fillMaxSize()) {
            Column(Modifier.fillMaxSize()) {
                ScreenHeader(
                    title = projectName,
                    onBack = onBack,
                    leading = { ctrl.selected?.let { StatusDot(stateColor(ctrl.states[it])); Spacer(Modifier.width(8.dp)) } },
                )
                TabRow(selectedTabIndex = tab) {
                    Tab(selected = tab == 0, onClick = { tab = 0 }, text = { Text("Terminal") })
                    Tab(selected = tab == 1, onClick = { tab = 1 }, text = { Text("Journal") })
                }
                Box(Modifier.weight(1f).fillMaxSize()) {
                    // Terminal: always composed, zero-size hidden when inactive.
                    Box(Modifier.fillMaxSize().then(if (tab == 0) Modifier else Modifier.size(0.dp))) {
                        TerminalBodyContent(ctrl, active = tab == 0, shortcuts, onShortcuts)
                    }
                    // Journal: also always composed so its list/scroll survives.
                    Box(Modifier.fillMaxSize().then(if (tab == 1) Modifier else Modifier.size(0.dp))) {
                        JournalScreen(server, projectID, onOpen = { n -> noteID = n.id })
                    }
                }
            }
            // Full-screen overlay: reading or editing a note while the workspace stays alive.
            noteID?.let { id ->
                NoteScreen(server, projectID, projectName, id, onBack = { noteID = null })
            }
        }
    }
}