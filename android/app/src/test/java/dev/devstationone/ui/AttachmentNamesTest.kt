package dev.devstationone.ui

import org.junit.Assert.assertEquals
import org.junit.Test

/** Attachment filenames come from the server; they become cache-file paths, so separators must die. */
class AttachmentNamesTest {
    @Test
    fun normalNamePassesThrough() {
        assertEquals("shot.png", safeAttName("shot.png"))
        assertEquals("report-2026 v2.pdf", safeAttName("report-2026 v2.pdf"))
    }

    @Test
    fun pathSeparatorsAndNulAreReplaced() {
        assertEquals(".._.._etc_passwd", safeAttName("../../etc/passwd"))
        assertEquals("a_b", safeAttName("a\\b"))
        assertEquals("x_y", safeAttName("x\u0000y"))
        assertEquals("_", safeAttName("/"))
    }

    @Test
    fun blankBecomesFallbackName() {
        assertEquals("attachment", safeAttName(""))
    }
}