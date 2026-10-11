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
import androidx.compose.foundation.layout.width
import androidx.compose.material3.Icon
import androidx.compose.material3.PrimaryTabRow
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Tab
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.layout.layout
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import dev.devstationone.data.Server
import dev.devstationone.data.ShortcutStore
import dev.devstationone.data.TabColorStore
import dev.devstationone.ui.kit.MuxIcons
import dev.devstationone.ui.kit.ScreenHeader
import dev.devstationone.ui.kit.StatusDot
import dev.devstationone.ui.kit.stateColor

/**
 * The project workspace: an icon-top tab bar over the project's surfaces
 * (docs/DESIGN_ANDROID_SCM.md OQ-9 / round 2): Terminal, Journal, Files,
 * Source Control, Databases (round 3). Icons are 24 dp `MuxIcons` strokes; each Tab's Icon carries
 * the a11y label, the selected state comes from Tab's semantics. The
 * documented fallback if icons-only proves unclear on device: text on the
 * selected tab.
 *
 * No tab is ever left the composition or re-measured: hidden tab
 * content is measured full-size (WebViews/PTY streams and list
 * state survive tab switches) but never placed — see [hiddenOther].
 */
@OptIn(androidx.compose.material3.ExperimentalMaterial3Api::class) // PrimaryTabRow
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
    var noteStartEdit by remember { mutableStateOf(false) }
    var filePath by remember { mutableStateOf<String?>(null) }
    var diff by remember { mutableStateOf<Pair<String, Boolean>?>(null) }

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
                PrimaryTabRow(selectedTabIndex = tab) {
                    Tab(selected = tab == 0, onClick = { tab = 0 }, icon = { Icon(MuxIcons.Prompt, "Terminal") })
                    Tab(selected = tab == 1, onClick = { tab = 1 }, icon = { Icon(MuxIcons.Note, "Journal") })
                    Tab(selected = tab == 2, onClick = { tab = 2 }, icon = { Icon(MuxIcons.Folder, "Files") })
                    Tab(selected = tab == 3, onClick = { tab = 3 }, icon = { Icon(MuxIcons.Branch, "Source Control") })
                    Tab(selected = tab == 4, onClick = { tab = 4 }, icon = { Icon(MuxIcons.Database, "Databases") })
                }
                Box(Modifier.weight(1f).fillMaxSize()) {
                    Box(Modifier.fillMaxSize().hiddenOther(tab != 0)) {
                        TerminalBodyContent(ctrl, active = tab == 0, shortcuts, onShortcuts)
                    }
                    Box(Modifier.fillMaxSize().hiddenOther(tab != 1)) {
                        JournalScreen(server, projectID, onOpen = { n, startEdit ->
                            noteID = n.id
                            noteStartEdit = startEdit
                        })
                    }
                    Box(Modifier.fillMaxSize().hiddenOther(tab != 2)) {
                        FileTreeScreen(server, projectID, onOpenFile = { filePath = it })
                    }
                    Box(Modifier.fillMaxSize().hiddenOther(tab != 3)) {
                        ScmScreen(server, projectID, onOpenDiff = { path, staged -> diff = path to staged })
                    }
                    Box(Modifier.fillMaxSize().hiddenOther(tab != 4)) {
                        DatabasesScreen(server, projectID, active = tab == 4)
                    }
                }
            }
            // Full-screen overlays: the workspace stays alive underneath.
            noteID?.let { id ->
                NoteScreen(server, projectID, projectName, id, startEdit = noteStartEdit, onBack = { noteID = null })
            }
            filePath?.let { p ->
                CodeScreen(server, projectID, projectName, p, onBack = { filePath = null })
            }
            diff?.let { (path, staged) ->
                DiffScreen(server, projectID, path, staged, onBack = { diff = null })
            }
        }
    }
}

/**
 * Measures [hidden] content at full size but never places it — an unplaced
 * placeable draws nothing, hit-tests nothing, and drops from the a11y tree.
 * This is what keeps hidden terminal WebViews perfectly sized/stateful while
 * making the other tab's content impossible to see "behind the tab row".
 */
private fun Modifier.hiddenOther(hidden: Boolean): Modifier = then(
    if (hidden) Modifier.layout { measurable, constraints ->
        measurable.measure(constraints)
        layout(0, 0) { /* not placed → not drawn */ }
    } else Modifier
)