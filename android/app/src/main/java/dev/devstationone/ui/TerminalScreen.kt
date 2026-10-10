package dev.devstationone.ui

import android.content.ClipboardManager
import android.content.Context
import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ArrowDropDown
import androidx.compose.material.icons.filled.Check
import dev.devstationone.data.Shortcut
import dev.devstationone.net.ConnState
import dev.devstationone.ui.kit.MuxChip
import dev.devstationone.ui.kit.MuxIcons
import dev.devstationone.ui.kit.RoundIconButton
import dev.devstationone.ui.kit.TabChipColors
import dev.devstationone.ui.kit.TabShape
import dev.devstationone.ui.kit.stateColor
import dev.devstationone.ui.theme.Mux
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/**
 * Terminal tab content of the project workspace ([WorkspaceScreen] owns the
 * header and the top tab bar, and hides this whole subtree when another
 * workspace tab shows — the WebViews/PTY streams here survive untouched).
 * The pane for the selected session is GONE when [active] is false, so no
 * hidden pane holds keyboard focus or paints.
 */
@Composable
fun TerminalBodyContent(
    ctrl: TerminalController,
    active: Boolean,
    shortcuts: List<Shortcut>,
    onShortcuts: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val ctx = LocalContext.current
    var fontSize by remember { mutableIntStateOf(14) }
    var menu by remember { mutableStateOf(false) }
    var newTab by remember { mutableStateOf(false) }
    var closing by remember { mutableStateOf<String?>(null) }
    var renaming by remember { mutableStateOf<String?>(null) }
    var menuTab by remember { mutableStateOf<String?>(null) }
    var colorMenu by remember { mutableStateOf(false) }
    var bulk by remember { mutableStateOf<BulkClose?>(null) }

    Column(modifier.fillMaxSize()) {
        // the terminal's action row (was TerminalScreen's header actions)
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End, verticalAlignment = Alignment.CenterVertically) {
            RoundIconButton(MuxIcons.Plus, "New session") { newTab = true }
            Spacer(Modifier.width(8.dp))
            Box {
                RoundIconButton(MuxIcons.More, "Menu") { menu = true }
                DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                    val sel = ctrl.selected
                    DropdownMenuItem(text = { Text("Shortcuts") }, onClick = { menu = false; onShortcuts() })
                    DropdownMenuItem(text = { Text("Paste") }, onClick = {
                        menu = false
                        val cm = ctx.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
                        val t = cm.primaryClip?.getItemAt(0)?.coerceToText(ctx)?.toString().orEmpty()
                        if (sel != null && t.isNotEmpty()) ctrl.enc(sel).paste(t)
                    })
                    DropdownMenuItem(text = { Text("Copy output → clipboard (selection)") }, onClick = { menu = false })
                    DropdownMenuItem(text = { Text("Reconnect") }, onClick = { menu = false; ctrl.kick() })
                    DropdownMenuItem(text = { Text("Refresh sessions") }, onClick = { menu = false; ctrl.load() })
                    DropdownMenuItem(text = { Text("Close tab…") }, onClick = { menu = false; closing = sel })
                }
            }
        }
        ctrl.error?.let {
            Text(it, color = MaterialTheme.colorScheme.error, fontSize = 12.sp, modifier = Modifier.padding(8.dp))
        }
            if (ctrl.tabs.isNotEmpty()) {
                val idx = ctrl.tabs.indexOfFirst { it.id == ctrl.selected }.coerceAtLeast(0)
                val tabState = rememberLazyListState()
                LazyRow(
                    state = tabState,
                    contentPadding = PaddingValues(horizontal = 16.dp, vertical = 8.dp),
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    items(ctrl.tabs, key = { it.id }) { s ->
                        val sel = s.id == ctrl.selected
                        Box {
                            MuxChip(
                                s.name.ifEmpty { "session" }, sel, { ctrl.selected = s.id },
                                dot = if (sel) null else stateColor(ctrl.states[s.id]),
                                onLongClick = { ctrl.selected = s.id; colorMenu = false; menuTab = s.id },
                                color = ctrl.tabColors[s.id]?.let { Color(it) },
                                shape = TabShape,
                            )
                            DropdownMenu(expanded = menuTab == s.id, onDismissRequest = { menuTab = null; colorMenu = false }) {
                                val i = ctrl.tabs.indexOfFirst { it.id == s.id }
                                fun closeBulk(title: String, ids: List<String>) {
                                    menuTab = null
                                    if (ids.isNotEmpty()) bulk = BulkClose(title, ids)
                                }
                                DropdownMenuItem(text = { Text("Rename") }, onClick = { menuTab = null; renaming = s.id })
                                DropdownMenuItem(
                                    text = { Text("Color") },
                                    trailingIcon = {
                                        Icon(Icons.Filled.ArrowDropDown, null)
                                        DropdownMenu(expanded = colorMenu, onDismissRequest = { colorMenu = false }) {
                                            DropdownMenuItem(
                                                text = { Text("No color") },
                                                leadingIcon = { if (ctrl.tabColors[s.id] == null) Icon(Icons.Filled.Check, null) },
                                                onClick = { ctrl.setColor(s.id, 0); menuTab = null; colorMenu = false },
                                            )
                                            for ((n, argb) in TabChipColors) {
                                                DropdownMenuItem(
                                                    text = { Text(n) },
                                                    leadingIcon = { if (ctrl.tabColors[s.id] == argb) Icon(Icons.Filled.Check, null) },
                                                    onClick = { ctrl.setColor(s.id, argb); menuTab = null; colorMenu = false },
                                                )
                                            }
                                        }
                                    },
                                    onClick = { colorMenu = true },
                                )
                                DropdownMenuItem(text = { Text("Close others") }, onClick = { closeBulk("Close other tabs", ctrl.tabs.filter { it.id != s.id }.map { it.id }) })
                                DropdownMenuItem(text = { Text("Close to the left") }, onClick = { closeBulk("Close tabs to the left", ctrl.tabs.take(i).map { it.id }) })
                                DropdownMenuItem(text = { Text("Close to the right") }, onClick = { closeBulk("Close tabs to the right", ctrl.tabs.drop(i + 1).map { it.id }) })
                            }
                        }
                    }
                }
            }
            Box(Modifier.weight(1f).fillMaxWidth()) {
                ctrl.tabs.forEach { s ->
                    key(s.id) {
                        TerminalPane(
                            conn = ctrl.conn(s.id),
                            enc = ctrl.enc(s.id),
                            visible = active && s.id == ctrl.selected,
                            fontSize = fontSize,
                            modifier = Modifier.fillMaxSize(),
                        )
                    }
                }
                if (ctrl.tabs.isEmpty()) {
                    Text("No sessions. Tap +.", color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.align(Alignment.Center))
                }
                ctrl.selected?.let { ShortcutDock(ctrl.enc(it), shortcuts, Modifier.fillMaxSize()) }
            }
    }

    if (newTab) {
        var name by remember { mutableStateOf("") }
        AlertDialog(
            onDismissRequest = { newTab = false },
            title = { Text("New terminal session") },
            text = {
                OutlinedTextField(
                    value = name,
                    onValueChange = { name = it.trim() },
                    label = { Text("Name, e.g. bash, Claude Code, dev server") },
                    singleLine = true,
                )
            },
            confirmButton = {
                TextButton(enabled = name.isNotBlank(), onClick = { ctrl.newTab(name); newTab = false }) { Text("Open") }
            },
            dismissButton = { TextButton(onClick = { newTab = false }) { Text("Cancel") } },
        )
    }

    closing?.let { id ->
        val name = ctrl.tabs.firstOrNull { it.id == id }?.name ?: "session"
        AlertDialog(
            onDismissRequest = { closing = null },
            title = { Text("Close “$name”") },
            text = { Text("Detach keeps the session's process running on the runtime. Kill ends it and everything in it.") },
            confirmButton = {
                TextButton(onClick = { ctrl.closeTab(id, kill = true); closing = null }) { Text("Kill") }
            },
            dismissButton = {
                Row {
                    TextButton(onClick = { closing = null }) { Text("Cancel") }
                    TextButton(onClick = { ctrl.closeTab(id, kill = false); closing = null }) { Text("Detach") }
                }
            },
        )
    }

    renaming?.let { id ->
        val orig = ctrl.tabs.firstOrNull { it.id == id }?.name.orEmpty()
        var name by remember(id) { mutableStateOf(orig) }
        AlertDialog(
            onDismissRequest = { renaming = null },
            title = { Text("Rename tab") },
            text = {
                OutlinedTextField(
                    value = name,
                    onValueChange = { name = it },
                    label = { Text("Session name") },
                    singleLine = true,
                )
            },
            confirmButton = {
                TextButton(enabled = name.isNotBlank() && name.trim() != orig, onClick = {
                    ctrl.renameTab(id, name)
                    renaming = null
                }) { Text("Rename") }
            },
            dismissButton = { TextButton(onClick = { renaming = null }) { Text("Cancel") } },
        )
    }

    bulk?.let { b ->
        AlertDialog(
            onDismissRequest = { bulk = null },
            title = { Text(b.title) },
            text = {
                Text("Detach ${b.ids.size} tab${if (b.ids.size == 1) "" else "s"}? Sessions keep running on the runtime and come back when refreshed.")
            },
            confirmButton = {
                TextButton(onClick = { b.ids.forEach { ctrl.closeTab(it, kill = false) }; bulk = null }) { Text("Detach") }
            },
            dismissButton = { TextButton(onClick = { bulk = null }) { Text("Cancel") } },
        )
    }
}

/** One bulk-detach confirmation: title + the session ids to detach. */
private data class BulkClose(val title: String, val ids: List<String>)

private class FanKey(
    val label: String, // short face text on the round button
    val name: String, // text label beside it
    val repeat: Boolean = false,
    val active: () -> Boolean = { false },
    val closes: Boolean = false, // close the fan after firing
    val act: () -> Unit,
)

private class FanGroup(val label: String, val name: String, val keys: List<FanKey>)

private const val BTN = 44 // dp, key button diameter
private const val STEP = 50 // dp, vertical spacing in a stack
private const val COL_W = 190 // dp, button + label
private const val EDGE = 8 // dp, gap between the bar and stacked keys
private const val BAR = 48 // dp, shortcut bar height

private fun fanGroups(enc: InputEncoder, shortcuts: List<Shortcut>): List<FanGroup> {
    fun r(label: String, name: String, f: () -> Unit) = FanKey(label, name, repeat = true, act = f)
    fun ctl(letter: Char, name: String) = FanKey("^$letter", name) { enc.raw((letter.code and 0x1f).toChar().toString()) }
    val fkeys = Key.entries.filter { it.name.length in 2..3 && it.name.startsWith("F") && it.name.drop(1).all(Char::isDigit) }
    return listOf(
        FanGroup("Mod", "Modifiers", listOf(
            FanKey("Ctrl", "Ctrl (sticky)", active = { enc.ctrl }) { enc.ctrl = !enc.ctrl },
            FanKey("Alt", "Alt (sticky)", active = { enc.alt }) { enc.alt = !enc.alt },
        )),
        FanGroup("^", "Ctrl combos", listOf(
            FanKey("Ctrl", "Ctrl (sticky)", active = { enc.ctrl }) { enc.ctrl = !enc.ctrl },
            FanKey("Alt", "Alt (sticky)", active = { enc.alt }) { enc.alt = !enc.alt },
            ctl('A', "Line start"), ctl('C', "Interrupt"), ctl('D', "End of input"), ctl('E', "Line end"),
            ctl('K', "Kill to end"), ctl('L', "Clear screen"), ctl('R', "History search"),
            ctl('U', "Kill line"), ctl('W', "Delete word"), ctl('Z', "Suspend"),
        )),
        FanGroup("|~", "Symbols", listOf(
            "|" to "Pipe", "~" to "Tilde", "-" to "Dash", "_" to "Underscore",
        ).map { (s, n) -> FanKey(s, n) { enc.text(s) } }),
        FanGroup("Nav", "Navigation", listOf(
            FanKey("Home", "Home") { enc.key(Key.HOME) }, FanKey("End", "End") { enc.key(Key.END) },
            r("PgUp", "Page up") { enc.key(Key.PGUP) }, r("PgDn", "Page down") { enc.key(Key.PGDN) },
            FanKey("Del", "Delete") { enc.key(Key.DELETE) },
        )),
        FanGroup("Fn", "F-keys", fkeys.map { f -> FanKey(f.name, f.name) { enc.key(f) } }),
        FanGroup("🤖", "Shortcuts", shortcuts.map { s ->
            FanKey(s.label, s.text, closes = true) { enc.text(s.text); if (s.enter) enc.key(Key.ENTER) }
        }),
    )
}

/**
 * Full-width shortcut bar over the terminal's bottom edge — Esc, Tab, /, :, the arrows —
 * with ▲ opening the drill-in stack (first groups, then the group's keys) above the bar.
 * Stacks too tall for the screen continue in a column to the right. The dock stays open
 * (sticky Ctrl/Alt, held arrows) until the toggle or the dimmed terminal is tapped.
 */
@Composable
fun ShortcutDock(enc: InputEncoder, shortcuts: List<Shortcut>, modifier: Modifier = Modifier) {
    var open by remember { mutableStateOf(false) }
    var group by remember { mutableStateOf<FanGroup?>(null) }
    val groups = remember(enc, shortcuts) { fanGroups(enc, shortcuts) }
    fun close() { open = false; group = null }

    BoxWithConstraints(modifier) {
        if (open) {
            Box(Modifier.fillMaxSize().background(Color(0x66000000)).pointerInput(Unit) { detectTapGestures(onTap = { close() }) })
        }
        val g = group
        val items: List<FanKey> = if (open) g?.keys ?: groups.map { fg -> FanKey(fg.label, fg.name) { group = fg } } else emptyList()
        val perCol = ((maxHeight.value - EDGE - BAR - EDGE) / STEP).toInt().coerceAtLeast(1)
        items.forEachIndexed { i, item ->
            FanRow(
                item,
                Modifier.align(Alignment.BottomStart).offset(
                    x = (EDGE + (i / perCol) * COL_W).dp,
                    y = -(EDGE + BAR + EDGE + (i % perCol) * STEP).dp,
                ),
                if (item.closes) { { item.act(); close() } } else item.act,
            )
        }
        Row(
            Modifier.align(Alignment.BottomCenter).fillMaxWidth().height(BAR.dp).background(MaterialTheme.colorScheme.surfaceVariant),
        ) {
            @Composable
            fun cell(label: String, repeat: Boolean = false, act: () -> Unit) = Box(
                Modifier.weight(1f).fillMaxHeight().fanPress(repeat, act),
                contentAlignment = Alignment.Center,
            ) { Text(label, color = MaterialTheme.colorScheme.onSurface, fontSize = 15.sp) }
            cell("ESC") { enc.key(Key.ESC) }
            cell("TAB") { enc.key(Key.TAB) }
            cell("/") { enc.text("/") }
            cell(":") { enc.text(":") }
            cell("↑", true) { enc.key(Key.UP) }
            cell("↓", true) { enc.key(Key.DOWN) }
            cell("←", true) { enc.key(Key.LEFT) }
            cell("→", true) { enc.key(Key.RIGHT) }
            val stuck = enc.ctrl || enc.alt
            Box(
                Modifier.weight(1f).fillMaxHeight()
                    .background(if (stuck) Mux.colors.mint else Color.Transparent)
                    .fanPress(false) { if (!open) open = true else if (g != null) group = null else close() },
                contentAlignment = Alignment.Center,
            ) {
                Text(
                    if (!open) "▲" else if (g != null) "‹" else "▼",
                    color = if (stuck) Mux.colors.onMint else MaterialTheme.colorScheme.onSurface,
                    fontSize = 15.sp,
                )
            }
        }
    }
}

/** Press fires [onPress] immediately; with [repeat] it keeps firing while held. */
@Composable
private fun Modifier.fanPress(repeat: Boolean, onPress: () -> Unit): Modifier {
    val scope = rememberCoroutineScope()
    val current by rememberUpdatedState(onPress)
    return pointerInput(repeat) {
        detectTapGestures(onPress = {
            current()
            if (!repeat) return@detectTapGestures
            val job = scope.launch {
                delay(400)
                while (true) {
                    current()
                    delay(60)
                }
            }
            tryAwaitRelease()
            job.cancel()
        })
    }
}

// The whole row (button and label) is the touch target; a label that ignores touches
// would fall through to the scrim and close the fan.
@Composable
private fun FanRow(item: FanKey, modifier: Modifier, onPress: () -> Unit) {
    Row(modifier.fanPress(item.repeat, onPress), verticalAlignment = Alignment.CenterVertically) {
        FanButton(item.label, item.active(), Modifier)
        Text(
            item.name,
            color = Color.White,
            fontSize = 14.sp,
            maxLines = 1,
            modifier = Modifier
                .padding(start = 6.dp)
                .clip(RoundedCornerShape(6.dp))
                .background(Color(0xE6151320))
                .padding(horizontal = 8.dp, vertical = 4.dp),
        )
    }
}

@Composable
private fun FanButton(label: String, active: Boolean, modifier: Modifier, size: Int = BTN) {
    Box(
        modifier
            .size(size.dp)
            .clip(CircleShape)
            .background(if (active) MaterialTheme.colorScheme.primary else Color(0xFF2A2536)),
        contentAlignment = Alignment.Center,
    ) {
        Text(label, color = if (active) MaterialTheme.colorScheme.onPrimary else Color.White, fontSize = if (size > BTN) 20.sp else 12.sp)
    }
}