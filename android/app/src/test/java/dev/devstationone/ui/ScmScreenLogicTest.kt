package dev.devstationone.ui

import dev.devstationone.net.GitBranchStatus
import dev.devstationone.net.GitFileStatus
import dev.devstationone.net.GitStatus
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** docs/DESIGN_ANDROID_SCM.md §4: the status→grouping mapping and the
 *  button-enabled derivations. US-SC1's grouped lists come from here. */
class ScmScreenLogicTest {

    private fun f(path: String, index: String = ".", worktree: String = ".", orig: String = "") =
        GitFileStatus(path = path, orig_path = orig, index = index, worktree = worktree)

    private fun rowsByChip(g: ScmGroups): Map<String, ScmRow> =
        (g.staged + g.changed).associateBy { it.file.path }

    @Test
    fun untrackedListsUnderChangesWithQuestionChip() {
        val g = groupScm(listOf(f("b.txt", "?", "?")))
        assertTrue(g.staged.isEmpty())
        assertEquals(1, g.changed.size)
        assertEquals("?", g.changed[0].chip)
        assertEquals("b.txt", g.changed[0].label)
    }

    @Test
    fun stagedAndWorktreeHalvesLandInTheirOwnGroups() {
        val g = groupScm(
            listOf(
                f("staged.txt", "A", "."), // added to the index, worktree clean
                f("work.txt", ".", "M"),   // unstaged modification only
                f("both.txt", "M", "M"),   // staged and then modified again
            ),
        )
        assertEquals(listOf("staged.txt", "both.txt"), g.staged.map { it.file.path })
        assertEquals(listOf("A", "M"), g.staged.map { it.chip })
        assertEquals(listOf("work.txt", "both.txt"), g.changed.map { it.file.path })
        assertEquals(listOf("M", "M"), g.changed.map { it.chip })
    }

    @Test
    fun renameShowsBothChipAndArrow() {
        val g = groupScm(listOf(f("new.txt", "R", ".", orig = "old.txt")))
        assertEquals(1, g.staged.size)
        assertEquals("old.txt → new.txt", g.staged[0].label)
        assertEquals("R", g.staged[0].chip)
    }

    @Test
    fun conflictShowsOnceInChanges() {
        val g = groupScm(listOf(f("a.txt", "U", "U")))
        assertTrue(g.staged.isEmpty())
        assertEquals(1, g.changed.size)
        assertEquals("U", g.changed[0].chip)
    }

    @Test
    fun deletedAndTypeChangeChipsMapVerbatim() {
        val g = groupScm(listOf(f("gone.txt", "D", "."), f("weird.txt", "T", ".")))
        assertEquals(listOf("D", "T"), g.staged.map { it.chip })
    }

    @Test
    fun syncButtonsFollowHasRemote() {
        assertTrue(syncEnabled(GitBranchStatus(name = "main", upstream = "origin/main", has_remote = true)))
        assertFalse(syncEnabled(GitBranchStatus(name = "main", has_remote = false))) // never pushed
        assertFalse(syncEnabled(null))
    }

    @Test
    fun commitButtonsDeriveFromGroupsAndMessage() {
        val empty = groupScm(emptyList())
        val mixed = groupScm(
            listOf(f("a.txt", "A", "."), f("b.txt", "?", "?")),
        )
        // Plain commit: staged, non-empty message.
        assertFalse(commitEnabled(empty, all = false, message = "x"))
        assertFalse(commitEnabled(empty, all = false, message = ""))
        assertFalse(commitEnabled(empty, all = true, message = "x"))
        assertTrue(commitEnabled(mixed, all = false, message = "x"))
        assertTrue(commitEnabled(mixed, all = true, message = "x"))
        assertTrue(commitEnabled(groupScm(listOf(f("b.txt", "?", "?"))), all = true, message = "x"))
        assertFalse(commitEnabled(groupScm(listOf(f("b.txt", "?", "?"))), all = false, message = "x"))
        // Nothing changed at all → neither button.
        assertFalse(commitEnabled(empty, all = true, message = ""))
        // rowsByChip is a compile-shape check: staged and changed rows are keyed together
        assertEquals(2, rowsByChip(mixed).size)
    }

    @Test
    fun statusPayloadSurvivesMissingOptionals() {
        // server omits upstream/ahead/behind for an unpushed branch; branch name exists
        val s = GitStatus(files = emptyList(), branch = GitBranchStatus(name = "main"))
        val g = groupScm(s.files)
        assertFalse(syncEnabled(s.branch))
        assertTrue(g.staged.isEmpty() && g.changed.isEmpty())
    }

    @Test
    fun discardOffersTrackedWorktreeChangesOnly() {
        val g = groupScm(listOf(f("m.txt", ".", "M"), f("new.txt", "?", "?"), f("c.txt", "U", "U"), f("s.txt", "A", ".")))
        assertEquals(listOf("m.txt"), discardable(g).map { it.file.path })
    }

    @Test
    fun checkoutNeedsACleanKnownStatus() {
        assertFalse(checkoutEnabled(null))
        assertTrue(checkoutEnabled(GitStatus()))
        assertFalse(checkoutEnabled(GitStatus(files = listOf(f("a.txt", "?", "?")))))
    }

    @Test
    fun amendNeedsHistoryAndMessage() {
        assertTrue(amendEnabled(true, "x"))
        assertFalse(amendEnabled(false, "x"))
        assertFalse(amendEnabled(true, " "))
    }
}
