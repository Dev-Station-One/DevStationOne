package dev.devstationone.net

import android.util.Base64
import dev.devstationone.data.DeviceKey
import dev.devstationone.data.Server
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.MediaType.Companion.toMediaTypeOrNull
import okhttp3.MultipartBody
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import org.json.JSONObject
import java.io.OutputStream
import java.security.SecureRandom
import java.util.concurrent.TimeUnit

/**
 * Signed-request auth for the DevStation Control Server (ADR-053): every
 * request carries
 * `Authorization: Sig id="<device>", ts="<unix>", nonce="<b64url>", sig="<b64 DER>"`
 * signed over "devstationone-v1\nMETHOD\nHOST\nREQUEST_URI\nts\nnonce".
 * The phone clock must be within 60s of the server's.
 */
object Auth {
    private val rnd = SecureRandom()

    fun header(server: Server, method: String, url: HttpUrl): String {
        val host = if (url.port == HttpUrl.defaultPort(url.scheme)) url.host else "${url.host}:${url.port}"
        val uri = url.encodedPath + (url.encodedQuery?.let { "?$it" } ?: "")
        val ts = (System.currentTimeMillis() / 1000).toString()
        val nonce = Base64.encodeToString(
            ByteArray(12).also { rnd.nextBytes(it) },
            Base64.URL_SAFE or Base64.NO_WRAP or Base64.NO_PADDING
        )
        val msg = "devstationone-v1\n$method\n$host\n$uri\n$ts\n$nonce".toByteArray()
        // The Keystore alias is keyed on the locally generated id, not the
        // server-side device row (server.deviceId only labels the Sig header).
        val sig = Base64.encodeToString(DeviceKey.sign(server.id, msg), Base64.NO_WRAP)
        return "Sig id=\"${server.deviceId}\", ts=\"$ts\", nonce=\"$nonce\", sig=\"$sig\""
    }
}

@Serializable
data class ProjectInfo(val id: String, val name: String, val runtime_id: String = "", val active_sessions: Int = 0)

@Serializable
data class RuntimeInfo(val id: String, val name: String, val online: Boolean = false)

@Serializable
data class SessionInfo(
    val id: String,
    val name: String = "",
    val status: String = "requested",
    val cols: Int = 80,
    val rows: Int = 24,
)

// ---- project journal (server-side notes; body is Summernote HTML, sanitized client-side) ----

@Serializable
data class JournalAttachment(
    val id: String,
    val filename: String,
    val content_type: String = "",
    val size_bytes: Long = 0,
    val created_at: String = "",
)

/** A note as the list returns it: snippet only, plus attachment metadata for the count. */
@Serializable
data class JournalNoteSummary(
    val id: String,
    val snippet: String = "",
    val attachments: List<JournalAttachment> = emptyList(),
    val commit_sha: String = "",
    val commit_subject: String = "",
    val commit_author: String = "",
    val commit_datetime: String? = null,
    val created_by: String = "",
    val creator_name: String = "",
    val created_at: String = "",
    val updated_at: String = "",
)

/** Full note: body_html + attachments. */
@Serializable
data class JournalNote(
    val id: String,
    val body_html: String = "",
    val attachments: List<JournalAttachment> = emptyList(),
    val commit_sha: String = "",
    val commit_subject: String = "",
    val commit_author: String = "",
    val commit_datetime: String? = null,
    val created_by: String = "",
    val creator_name: String = "",
    val created_at: String = "",
    val updated_at: String = "",
)

@Serializable
data class JournalPage(val notes: List<JournalNoteSummary>, val total: Int = 0, val limit: Int = 0, val offset: Int = 0)

// ---- project files (live RPCs to the runtime; paths are project-root-relative) ----

/** One filesystem entry as the runtime sees it. */
@Serializable
data class FileInfo(
    val name: String,
    val type: String = "file", // file | dir | symlink | other
    val size: Long = 0,
    val mode: String = "",
    val mtime: String = "",
    val owner: String? = null,
    val group: String? = null,
    val owner_id: Int = 0,
    val group_id: Int = 0,
)

class ApiException(val code: Int, message: String) : Exception(message)

class Api(private val server: Server) {
    private val base = server.url.trimEnd('/').toHttpUrl()

    companion object {
        private val json = Json { ignoreUnknownKeys = true }
        private val jsonMime = "application/json".toMediaType()
        val client: OkHttpClient = OkHttpClient.Builder()
            .connectTimeout(15, TimeUnit.SECONDS)
            .readTimeout(60, TimeUnit.SECONDS)
            .writeTimeout(60, TimeUnit.SECONDS)
            .build()
        val wsClient: OkHttpClient = client.newBuilder()
            .readTimeout(0, TimeUnit.MILLISECONDS)
            .pingInterval(20, TimeUnit.SECONDS)
            .build()

        /** Exchanges a pairing code + the device's public key for a device id. */
        fun pair(baseUrl: String, code: String, pubKey: String): String {
            val url = baseUrl.trimEnd('/').toHttpUrl().newBuilder().addPathSegments("api/v1/auth/devices/pair").build()
            val body = buildJsonObject {
                put("code", code)
                put("public_key", pubKey)
            }.toString().toRequestBody(jsonMime)
            client.newCall(Request.Builder().url(url).post(body).build()).execute().use { r ->
                val text = r.body?.string().orEmpty()
                if (!r.isSuccessful) throw ApiException(r.code, text.trim().ifEmpty { "HTTP ${r.code}" })
                return JSONObject(text).getString("id")
            }
        }
    }

    fun url(vararg segs: String): HttpUrl =
        base.newBuilder().apply { segs.forEach { addPathSegment(it) } }.build()

    private fun request(method: String, url: HttpUrl, body: RequestBody? = null): Request =
        Request.Builder().url(url).method(method, body)
            .header("Authorization", Auth.header(server, method, url)).build()

    private fun bodyOf(vararg pairs: Pair<String, String>): RequestBody =
        buildJsonObject { pairs.forEach { (k, v) -> put(k, v) } }.toString().toRequestBody(jsonMime)

    private fun Response.jsonText(): String = body?.string().orEmpty()

    private fun check(r: Response, want: IntRange = 200..299) {
        if (r.code !in want) throw ApiException(r.code, r.jsonText().trim().ifEmpty { "HTTP ${r.code}" })
    }

    // ---- projects (read-only; creation stays on the web) ----

    fun projects(): List<ProjectInfo> {
        client.newCall(request("GET", url("api", "v1", "projects"))).execute().use {
            check(it)
            return json.decodeFromString(it.jsonText())
        }
    }

    fun runtimes(): List<RuntimeInfo> {
        client.newCall(request("GET", url("api", "v1", "runtimes"))).execute().use {
            check(it)
            return json.decodeFromString(it.jsonText())
        }
    }

    // ---- terminal sessions ----

    /** All project sessions, including exited ones (tabs filter terminal states out). */
    fun sessions(projectID: String): List<SessionInfo> {
        client.newCall(request("GET", url("api", "v1", "projects", projectID, "sessions"))).execute().use {
            check(it)
            return json.decodeFromString(it.jsonText())
        }
    }

    fun session(id: String): SessionInfo {
        client.newCall(request("GET", url("api", "v1", "sessions", id))).execute().use {
            check(it)
            return json.decodeFromString(it.jsonText())
        }
    }

    fun createSession(projectID: String, name: String, cols: Int, rows: Int): SessionInfo {
        val body = """{"name":"$name","cols":$cols,"rows":$rows}""".toRequestBody(jsonMime)
        client.newCall(request("POST", url("api", "v1", "projects", projectID, "sessions"), body)).execute().use {
            check(it)
            return json.decodeFromString(it.jsonText())
        }
    }

    /** Renames the session; the server answers 400 on a blank name. */
    fun rename(id: String, to: String) {
        client.newCall(request("PATCH", url("api", "v1", "sessions", id), bodyOf("name" to to))).execute().use { check(it) }
    }

    /** Ends the session's PTY and its process. */
    fun kill(id: String) {
        client.newCall(request("DELETE", url("api", "v1", "sessions", id))).execute().use { check(it) }
    }

    fun attach(id: String) {
        client.newCall(request("POST", url("api", "v1", "sessions", id, "attach"))).execute().use { check(it) }
    }

    /** Mark the session detached; its process keeps running. */
    fun detach(id: String) {
        client.newCall(request("POST", url("api", "v1", "sessions", id, "detach"))).execute().use { check(it) }
    }

    fun resize(id: String, cols: Int, rows: Int) {
        val body = """{"cols":$cols,"rows":$rows}""".toRequestBody(jsonMime)
        client.newCall(request("POST", url("api", "v1", "sessions", id, "resize"), body)).execute().use { check(it) }
    }

    fun signal(id: String, name: String) {
        client.newCall(request("POST", url("api", "v1", "sessions", id, "signal"), bodyOf("signal" to name))).execute().use { check(it) }
    }

    // ---- project journal ----

    /** One page of the project's journal, newest first. */
    fun journal(projectID: String, limit: Int = 25, offset: Int = 0): JournalPage {
        val u = url("api", "v1", "projects", projectID, "journal").newBuilder()
            .addQueryParameter("limit", limit.toString())
            .addQueryParameter("offset", offset.toString())
            .build()
        client.newCall(request("GET", u)).execute().use {
            check(it)
            return json.decodeFromString(it.jsonText())
        }
    }

    /** Creates a note. The server rejects an empty body_html, so the seed is a blank paragraph. */
    fun createNote(projectID: String): JournalNote {
        client.newCall(request("POST", url("api", "v1", "projects", projectID, "journal"), bodyOf("body_html" to "<p></p>"))).execute().use {
            check(it)
            return json.decodeFromString(it.jsonText())
        }
    }

    fun note(projectID: String, noteID: String): JournalNote {
        client.newCall(request("GET", url("api", "v1", "projects", projectID, "journal", "notes", noteID))).execute().use {
            check(it)
            return json.decodeFromString(it.jsonText())
        }
    }

    /** Overwrites the note body; caller sends DOMPurify output, echoing the web client. */
    fun putNote(projectID: String, noteID: String, bodyHTML: String): JournalNote {
        client.newCall(request("PUT", url("api", "v1", "projects", projectID, "journal", "notes", noteID), bodyOf("body_html" to bodyHTML))).execute().use {
            check(it)
            return json.decodeFromString(it.jsonText())
        }
    }

    fun deleteNote(projectID: String, noteID: String) {
        client.newCall(request("DELETE", url("api", "v1", "projects", projectID, "journal", "notes", noteID))).execute().use { check(it) }
    }

    /** Uploads one attachment (multipart field "file"); the server caps 25 MB / 10 per note. */
    fun uploadAttachment(projectID: String, noteID: String, filename: String, mime: String, bytes: ByteArray): JournalAttachment {
        val type = if (mime.isBlank()) null else mime.toMediaTypeOrNull()
        val body = MultipartBody.Builder().setType(MultipartBody.FORM)
            .addFormDataPart("file", filename, bytes.toRequestBody(type))
            .build()
        client.newCall(request("POST", url("api", "v1", "projects", projectID, "journal", "notes", noteID, "attachments"), body)).execute().use {
            check(it)
            return json.decodeFromString(it.jsonText())
        }
    }

    /** Streams the attachment bytes into [out]; the server always answers as a download. */
    fun downloadAttachment(projectID: String, noteID: String, attachmentID: String, out: OutputStream) {
        client.newCall(request("GET", url("api", "v1", "projects", projectID, "journal", "notes", noteID, "attachments", attachmentID))).execute().use {
            check(it)
            it.body?.byteStream()?.copyTo(out)
        }
    }

    /** Lists one directory. [path] is project-root-relative; "." lists the project root. */
    fun listFiles(projectID: String, path: String): List<FileInfo> {
        val u = url("api", "v1", "projects", projectID, "files").newBuilder()
            .addQueryParameter("path", path)
            .build()
        client.newCall(request("GET", u)).execute().use {
            check(it)
            return json.decodeFromString(it.jsonText())
        }
    }

    /** Case-insensitive name-substring search; returns root-relative paths. */
    fun searchFiles(projectID: String, query: String, path: String): List<String> {
        val u = url("api", "v1", "projects", projectID, "files", "search").newBuilder()
            .addQueryParameter("q", query)
            .addQueryParameter("path", path)
            .build()
        client.newCall(request("GET", u)).execute().use {
            check(it)
            return json.decodeFromString(it.jsonText())
        }
    }
}