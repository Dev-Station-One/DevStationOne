package dev.devstationone.ui

import dev.devstationone.net.FileInfo
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/** Path helpers behind the Files tab: breadcrumbs, jumps, and sort order. */
class FileTreePathsTest {
    @Test
    fun rootHasOneCrumbAndJumpsToDot() {
        assertEquals(listOf("root"), crumbs("."))
        assertEquals(listOf("root"), crumbs(""))
        assertEquals(".", subPath(".", 0))
        assertNull(subPath(".", 1))
    }

    @Test
    fun deepPathCrumbLabelsAndJumps() {
        assertEquals(listOf("root", "src", "main"), crumbs("src/main"))
        assertEquals(null, subPath("src/main", -1))
        assertEquals(".", subPath("src/main", 0))
        assertEquals("src", subPath("src/main", 1))
        assertEquals("src/main", subPath("src/main", 2))
        assertNull(subPath("src/main", 3))
    }

    @Test
    fun dirsSortBeforeFilesThenByName() {
        val l = listOf(
            FileInfo(name = "zeta.txt", type = "file"),
            FileInfo(name = "BETA", type = "dir"),
            FileInfo(name = "alpha", type = "dir"),
            FileInfo(name = "aardvark", type = "file"),
        )
        val names = dirsFirst(l).map { it.name }
        assertEquals(listOf("alpha", "BETA", "aardvark", "zeta.txt"), names)
    }

    @Test
    fun glyphsMatchType() {
        assertEquals("📁", fileGlyph("dir"))
        assertEquals("🔗", fileGlyph("symlink"))
        assertEquals("📄", fileGlyph("file"))
        assertEquals("📄", fileGlyph("other"))
    }
}