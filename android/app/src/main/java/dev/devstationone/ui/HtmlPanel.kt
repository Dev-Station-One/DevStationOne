package dev.devstationone.ui

import android.os.Handler
import android.os.Looper
import android.util.Base64
import android.view.View
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.viewinterop.AndroidView
import dev.devstationone.data.Server
import dev.devstationone.net.Api
import dev.devstationone.net.Auth
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.Request
import java.io.ByteArrayInputStream

private const val NOTE_PAGE = "file:///android_asset/note.html"

/**
 * Renders one note's `body_html` in a WebView. The note page sanitizes with
 * the same DOMPurify file the web client vendors — output is never trusted
 * by the app.
 *
 * Images inside the body reference server URLs (`/api/v1/...`) by absolute
 * path. Browser clients ride session cookies; this app signs every request
 * instead, so those loads are bridged through OkHttp with the Sig header —
 * no cookies cross from the app, and nothing else in the page can fetch
 * beyond that surface (file asset loads only otherwise).
 */
@Composable
fun HtmlPanel(server: Server, html: String, modifier: Modifier = Modifier) {
    val base = remember(server.id) { server.url.trimEnd('/').toHttpUrl() }
    var ready by remember { mutableStateOf(false) }
    var rendered by remember { mutableStateOf("") }

    AndroidView(
        modifier = modifier,
        factory = { c ->
            WebView(c).apply {
                settings.javaScriptEnabled = true
                settings.allowFileAccess = false // the android_asset page needs none
                settings.allowContentAccess = false
                setBackgroundColor(android.graphics.Color.TRANSPARENT)
                isVerticalScrollBarEnabled = false
                overScrollMode = View.OVER_SCROLL_NEVER
                webViewClient = object : WebViewClient() {
                    // the note page never navigates anywhere
                    override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest) = true

                    // Serve asset files normally; bridge the note's server-URL
                    // references through OkHttp with the device signature.
                    override fun shouldInterceptRequest(view: WebView, request: WebResourceRequest): WebResourceResponse? {
                        val path = request.url.path ?: return null
                        if (!path.startsWith("/api/v1/")) return null
                        val httpUrl = (base.toString() + path + (request.url.query?.let { "?$it" } ?: "")).toHttpUrl()
                        return try {
                            val resp = Api.client.newCall(
                                Request.Builder().url(httpUrl)
                                    .header("Authorization", Auth.header(server, "GET", httpUrl))
                                    .build()
                            ).execute()
                            val body = resp.body ?: return WebResourceResponse("text/plain", "utf-8", resp.code, resp.message, null, ByteArrayInputStream(ByteArray(0)))
                            val type = resp.header("Content-Type") ?: "application/octet-stream"
                            val mime = type.substringBefore(';').trim()
                            val charset = type.substringAfter("charset=", "").substringBefore(';').ifEmpty { null }
                            // WebView closes the stream when done reading, which closes the call.
                            WebResourceResponse(mime, charset, body.byteStream())
                        } catch (e: Exception) {
                            WebResourceResponse("text/plain", "utf-8", 502, "unreachable", null, ByteArrayInputStream(ByteArray(0)))
                        }
                    }
                }
                addJavascriptInterface(object {
                    @android.webkit.JavascriptInterface
                    fun ready() {
                        Handler(Looper.getMainLooper()).post { ready = true }
                    }
                }, "Android")
                loadUrl(NOTE_PAGE)
            }
        },
        update = { v ->
            if (ready && html != rendered) {
                rendered = html
                // base64 keeps quotes/HTML out of the JS string
                val b64 = Base64.encodeToString(html.toByteArray(), Base64.NO_WRAP)
                v.evaluateJavascript("setBody('$b64')", null)
            }
        },
        onRelease = { it.destroy() },
    )
}