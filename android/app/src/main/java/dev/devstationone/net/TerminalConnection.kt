package dev.devstationone.net

import dev.devstationone.data.Server
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.ByteString
import okio.ByteString.Companion.toByteString
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

enum class ConnState { CONNECTING, CONNECTED, RECONNECTING, EXITED }

/**
 * One PTY session streamed over the Control Server's per-session WebSocket
 * (docs/PROTOCOL.md: binary frames are raw terminal bytes both ways — no
 * JSON control channel; resize/signal ride the REST API). Reconnects with
 * backoff; the session lives server-side (sessions survive disconnects, and
 * reattach replays the server-side scrollback, so the terminal starts clean
 * like a fresh tmux attach).
 *
 * Callbacks arrive on OkHttp threads.
 */
class TerminalConnection(private val server: Server, var session: String) {
    var onOpen: (() -> Unit)? = null
    var onData: ((ByteArray) -> Unit)? = null
    var onState: ((ConnState) -> Unit)? = null

    @Volatile var cols = 80
        private set
    @Volatile var rows = 24
        private set

    private val base = server.url.trimEnd('/').toHttpUrl()
    private val api = Api(server)
    private val timer = Executors.newSingleThreadScheduledExecutor()
    private val lock = Any()
    private var ws: WebSocket? = null
    private var attempt = 0
    private var started = false
    private var closed = false

    fun start() {
        synchronized(lock) {
            if (started || closed) return
            started = true
        }
        connect()
    }

    private fun connect() {
        synchronized(lock) { if (closed) return }
        onState?.invoke(if (attempt == 0) ConnState.CONNECTING else ConnState.RECONNECTING)
        val url = base.newBuilder()
            .addPathSegments("api/v1/sessions/$session/stream")
            .build()
        val req = Request.Builder().url(url)
            .header("Authorization", Auth.header(server, "GET", url)).build()
        val socket = Api.wsClient.newWebSocket(req, Listener())
        synchronized(lock) { ws = socket }
    }

    private inner class Listener : WebSocketListener() {
        private fun current(w: WebSocket) = synchronized(lock) { w === ws }

        override fun onOpen(webSocket: WebSocket, response: Response) {
            if (!current(webSocket)) return
            attempt = 0
            onState?.invoke(ConnState.CONNECTED)
            onOpen?.invoke()
        }

        override fun onMessage(webSocket: WebSocket, bytes: ByteString) {
            if (current(webSocket)) onData?.invoke(bytes.toByteArray())
        }

        override fun onClosed(webSocket: WebSocket, code: Int, reason: String) = lost(webSocket)
        override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) = lost(webSocket)

        private fun lost(w: WebSocket) {
            if (!current(w)) return
            // The stream ends for two very different reasons: the session's
            // process exited (server tears the stream down) or the network
            // hiccupped. Ask the server which it is before reconnecting —
            // otherwise an exited session would reconnect-loop forever.
            timer.schedule({
                val status = runCatching { api.session(session).status }.getOrNull()
                if (status in setOf("exited", "closed", "failed")) {
                    synchronized(lock) { closed = true }
                    timer.shutdownNow()
                    onState?.invoke(ConnState.EXITED)
                } else {
                    scheduleReconnect()
                }
            }, 0, TimeUnit.MILLISECONDS)
        }
    }

    private fun scheduleReconnect() {
        synchronized(lock) { if (closed) return }
        attempt++
        onState?.invoke(ConnState.RECONNECTING)
        val delay = minOf(15_000L, 500L shl minOf(attempt, 5))
        try { timer.schedule({ connect() }, delay, TimeUnit.MILLISECONDS) } catch (_: IllegalStateException) {}
    }

    /** Reconnect immediately (e.g. network changed). No-op before start(). */
    fun kick() {
        synchronized(lock) {
            if (!started || closed) return
            ws?.cancel()
            ws = null
        }
        attempt = 0
        connect()
    }

    fun send(data: ByteArray) {
        synchronized(lock) { ws }?.send(data.toByteString())
    }

    fun resize(c: Int, r: Int) {
        if (c <= 0 || r <= 0) return
        cols = c
        rows = r
        // Resize rides the REST API (ADR-053 routes), debounced by pane code.
        timer.schedule({ runCatching { api.resize(session, c, r) } }, 0, TimeUnit.MILLISECONDS)
    }

    /** Send a POSIX signal by name (docs/API.md sessions/signal). */
    fun signal(name: String) {
        timer.schedule({ runCatching { api.signal(session, name) } }, 0, TimeUnit.MILLISECONDS)
    }

    /** Close the socket and mark the session detached; its process keeps running on the runtime. */
    fun close() {
        synchronized(lock) {
            closed = true
            ws?.close(1000, null)
            ws = null
        }
        onData = null
        onOpen = null
        onState = null
        timer.shutdownNow()
        runCatching { api.detach(session) }
    }
}