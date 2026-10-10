package dev.devstationone

import android.app.KeyguardManager
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.os.Bundle
import android.os.SystemClock
import android.view.WindowManager
import android.widget.Toast
import androidx.activity.result.contract.ActivityResultContracts
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.material3.Surface
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import dev.devstationone.data.AppLock
import dev.devstationone.data.AppSettings
import dev.devstationone.data.Server
import dev.devstationone.data.ServerStore
import dev.devstationone.data.ShortcutStore
import dev.devstationone.ui.theme.DevStationTheme
import dev.devstationone.ui.LockedScreen
import dev.devstationone.ui.PairScreen
import dev.devstationone.ui.ProjectsScreen
import dev.devstationone.ui.ServerListScreen
import dev.devstationone.ui.SettingsScreen
import dev.devstationone.ui.ShortcutsScreen
import dev.devstationone.ui.WorkspaceScreen

private sealed interface Screen {
    data object List : Screen
    data class Pair(val url: String = "", val code: String = "") : Screen
    data class Projects(val server: Server) : Screen
    data class Workspace(val server: Server, val projectID: String, val projectName: String) : Screen
    data object Shortcuts : Screen
    data object Settings : Screen
}

class MainActivity : ComponentActivity() {
    private var screen by mutableStateOf<Screen>(Screen.List)
    private var preShortcuts by mutableStateOf<Screen>(Screen.List)
    private lateinit var appLock: AppLock
    private lateinit var settings: AppSettings
    private var lockEnabled by mutableStateOf(false)
    private var allowScreenshots by mutableStateOf(false)
    private var editorWordwrap by mutableStateOf(true)
    private var editorFontSize by mutableStateOf(14)
    private var locked by mutableStateOf(false)
    private var promptOnStart = false
    private var pendingToggle = false
    private var stoppedAt: Long? = null

    private val credential = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { r ->
        stoppedAt = null // the prompt itself backgrounded us; don't count it as time away
        val ok = r.resultCode == RESULT_OK
        if (pendingToggle) {
            pendingToggle = false
            if (ok) { appLock.enabled = !appLock.enabled; lockEnabled = appLock.enabled }
        } else if (ok) locked = false
    }

    /** Device PIN/pattern/password prompt. Returns false when the phone has no screen lock. */
    private fun askCredential(): Boolean {
        val i = getSystemService(KeyguardManager::class.java).createConfirmDeviceCredentialIntent("DevStation", null) ?: return false
        credential.launch(i)
        return true
    }

    /** FLAG_SECURE hides the window from screenshots, screen recording and the recents thumbnail. */
    private fun applyScreenshotSetting() {
        if (allowScreenshots) window.clearFlags(WindowManager.LayoutParams.FLAG_SECURE)
        else window.setFlags(WindowManager.LayoutParams.FLAG_SECURE, WindowManager.LayoutParams.FLAG_SECURE)
    }

    /** Screen off (timeout or power button) locks at once, so waking the phone always asks again. */
    private val screenOff = object : BroadcastReceiver() {
        override fun onReceive(context: Context, intent: Intent) {
            if (appLock.enabled && !locked) { locked = true; promptOnStart = true }
        }
    }

    private fun unlock() {
        if (!askCredential()) locked = false // no screen lock left, so nothing to check against
    }

    /** Turning the lock on or off needs the credential too, or a thief could just switch it off. */
    private fun toggleLock() {
        pendingToggle = true
        if (!askCredential()) pendingToggle = false
    }

    override fun onStart() {
        super.onStart()
        val away = stoppedAt?.let { SystemClock.elapsedRealtime() - it > LOCK_GRACE_MS } ?: false
        stoppedAt = null
        if (appLock.enabled && away && !locked) { locked = true; promptOnStart = true }
        if (locked && promptOnStart) { promptOnStart = false; unlock() }
    }

    override fun onStop() {
        super.onStop()
        stoppedAt = SystemClock.elapsedRealtime()
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        appLock = AppLock(this)
        registerReceiver(screenOff, IntentFilter(Intent.ACTION_SCREEN_OFF))
        settings = AppSettings(this)
        allowScreenshots = settings.allowScreenshots
        editorWordwrap = settings.editorWordwrap
        editorFontSize = settings.editorFontSize
        applyScreenshotSetting()
        lockEnabled = appLock.enabled
        locked = lockEnabled // cold start always locks
        promptOnStart = locked
        val store = ServerStore(this)
        val shortcuts = ShortcutStore(this)
        handlePairIntent(intent)
        setContent {
            DevStationTheme {
                Surface {
                    if (locked) { LockedScreen(::unlock); return@Surface }
                    when (val s = screen) {
                        Screen.List -> ServerListScreen(
                            store, onSettings = { screen = Screen.Settings },
                            onOpen = { screen = Screen.Projects(it) },
                            onAdd = { screen = Screen.Pair() },
                        )
                        is Screen.Pair -> PairScreen(
                            store, s.url, s.code,
                            onDone = { screen = Screen.List },
                            onCancel = { screen = Screen.List },
                        )
                        is Screen.Projects -> ProjectsScreen(
                            s.server,
                            onBack = { screen = Screen.List },
                            onOpen = { p -> screen = Screen.Workspace(s.server, p.id, p.name) },
                        )
                        is Screen.Workspace -> WorkspaceScreen(
                            s.server, s.projectID, s.projectName,
                            onShortcuts = { preShortcuts = screen; screen = Screen.Shortcuts },
                            onBack = { screen = Screen.Projects(s.server) },
                        )
                        Screen.Shortcuts -> ShortcutsScreen(
                            shortcuts,
                            onBack = { screen = preShortcuts },
                        )
                        Screen.Settings -> SettingsScreen(
                            lockEnabled, ::toggleLock,
                            allowScreenshots, { allowScreenshots = it; settings.allowScreenshots = it; applyScreenshotSetting() },
                            editorWordwrap, { editorWordwrap = it; settings.editorWordwrap = it },
                            editorFontSize, { editorFontSize = it; settings.editorFontSize = it },
                            onBack = { screen = Screen.List },
                        )
                    }
                }
            }
        }
    }

    override fun onDestroy() {
        unregisterReceiver(screenOff)
        super.onDestroy()
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handlePairIntent(intent)
    }

    /** devstation://pair?url=...&code=... (from a link or QR opened by the system camera). */
    private fun handlePairIntent(i: Intent?) {
        val d = i?.data ?: return
        if (d.scheme == "devstation" && d.host == "pair") {
            // a link must not yank the user out of a live terminal or pairing flow
            if (screen !is Screen.List && screen !is Screen.Pair && screen !is Screen.Settings) {
                Toast.makeText(this, "Close the terminal first to pair", Toast.LENGTH_SHORT).show()
                return
            }
            screen = Screen.Pair(d.getQueryParameter("url").orEmpty(), d.getQueryParameter("code").orEmpty())
        }
    }

    private companion object {
        const val LOCK_GRACE_MS = 60_000L
    }
}