package dev.devstationone.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** The §4 collision table, the extension→scope map, and the binary sniff. */
class CodeScreenLogicTest {
    private val t0 = "2026-10-10T12:00:00Z"

    @Test
    fun collisionTable() {
        assertEquals("put", saveDecision(t0, t0, edited = false))
        assertEquals("put", saveDecision(t0, t0, edited = true))
        assertEquals("reload", saveDecision(t0, "later", edited = false))
        assertEquals("conflict", saveDecision(t0, "later", edited = true))
    }

    @Test
    fun extensionsMap() {
        assertEquals("source.kotlin", grammarFor("Main.kt"))
        assertEquals("source.kotlin", grammarFor("a/b/build.gradle.kts"))
        assertEquals("source.java", grammarFor("App.java"))
        assertEquals("source.yaml", grammarFor("ci.yml"))
        assertEquals("source.ts", grammarFor("index.ts"))
        assertEquals("source.ts", grammarFor("widget.tsx"))
        assertEquals("source.js", grammarFor("script.mjs"))
        assertEquals("text.html.basic", grammarFor("page.html"))
        assertEquals("text.html.markdown", grammarFor("README.md"))
        assertEquals("source.shell", grammarFor("deploy.sh"))
        assertEquals("source.dockerfile", grammarFor("dockerfile"))
        assertEquals("source.dockerfile", grammarFor("Dockerfile"))
        assertEquals("source.ini", grammarFor("app.conf"))
        assertEquals("source.cpp", grammarFor("main.cpp"))
        assertEquals("source.cs", grammarFor("Program.cs"))
        assertEquals("source.rust", grammarFor("lib.rs"))
        assertNull(grammarFor("Makefile")) // not in the subset — plain text
        assertNull(grammarFor("blob.bin"))
    }

    @Test
    fun nulByteMeansBinary() {
        assertTrue(looksBinary(byteArrayOf(0x50, 0x00, 0x4B)))
        assertFalse(looksBinary("plain text".toByteArray()))
        assertFalse(looksBinary(ByteArray(0)))
    }

    @Test
    fun previewScope() {
        assertTrue(previewable("README.md"))
        assertTrue(previewable("a/b/notes.markdown"))
        assertTrue(previewable("page.html"))
        assertTrue(previewable("Page.HTM"))
        assertFalse(previewable("build.gradle.kts"))
        assertFalse(previewable("notes.txt"))
        assertTrue(previewIsHtml("index.html"))
        assertFalse(previewIsHtml("README.md"))
    }
}