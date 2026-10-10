package dev.devstationone.net

import kotlinx.serialization.json.Json
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** Journal API model decoding against openapi.yaml shapes (unknown keys must be ignored). */
class JournalModelsTest {
    private val json = Json { ignoreUnknownKeys = true }

    @Test
    fun pageDecodes() {
        val p = json.decodeFromString<JournalPage>("""{"notes":[
            {"id":"n1","snippet":"hi <b>&amp;</b>","attachments":[],"created_at":"2026-10-10T10:00:00Z","updated_at":"2026-10-10T11:00:00Z","creator_name":"dev"},
            {"id":"n2","snippet":"two","commit_sha":"a1b2c3d4e5f6"}
        ],"total":9,"limit":25,"offset":0}""")
        assertEquals(9, p.total)
        assertEquals(2, p.notes.size)
        assertEquals("hi <b>&amp;</b>", p.notes[0].snippet)
        assertEquals("a1b2c3d4e5f6", p.notes[1].commit_sha)
        assertEquals(0, p.notes[1].attachments.size)
    }

    @Test
    fun noteDecodesBodyAndAttachments() {
        val n = json.decodeFromString<JournalNote>("""{"id":"n1","project_id":"p1","body_html":"<p>x</p>",
            "attachments":[{"id":"a1","filename":"snap.png","content_type":"image/png","size_bytes":12345,"created_at":"2026-10-10T10:00:00Z"}],
            "commit_sha":"","created_by":"u1","creator_name":"dev","created_at":"2026-10-10T10:00:00Z","updated_at":"2026-10-10T11:00:00Z",
            "someFieldFromTheFuture":true}""")
        assertEquals("<p>x</p>", n.body_html)
        assertEquals("snap.png", n.attachments[0].filename)
        assertEquals(12345, n.attachments[0].size_bytes)
        assertTrue(n.commit_sha.isEmpty())
    }
}