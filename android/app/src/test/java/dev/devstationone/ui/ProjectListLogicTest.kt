package dev.devstationone.ui

import dev.devstationone.net.ProjectInfo
import dev.devstationone.net.RuntimeInfo
import org.junit.Assert.assertEquals
import org.junit.Test

class ProjectListLogicTest {
    private fun p(id: String, name: String, rt: String) = ProjectInfo(id, name, rt)

    @Test
    fun sortedProjectsIsCaseInsensitiveAndStable() {
        val sorted = sortedProjects(listOf(p("3", "beta", "r"), p("2", "Alpha", "r"), p("1", "alpha", "r"), p("4", "Zed", "r")))
        assertEquals(listOf("1", "2", "3", "4"), sorted.map { it.id }) // alpha/Alpha tie broken by id
    }

    @Test
    fun runtimeOptionsOnlyRuntimesWithProjectsByName() {
        val rts = listOf(RuntimeInfo("a", "zeta", true), RuntimeInfo("b", "Alpha", false), RuntimeInfo("c", "idle", true))
        val projects = listOf(p("1", "x", "a"), p("2", "y", "b"), p("3", "z", "gone"))
        assertEquals(listOf("b", "a"), runtimeOptions(projects, rts).map { it.id })
    }

    @Test
    fun filterProjectsAndUnknownStoredRuntimeFallsBackToAll() {
        val projects = listOf(p("1", "x", "a"), p("2", "y", "b"), p("3", "z", "unknown"))
        val options = runtimeOptions(projects, listOf(RuntimeInfo("a", "A"), RuntimeInfo("b", "B")))
        assertEquals(listOf("1"), filterProjects(projects, "a").map { it.id })
        assertEquals(3, filterProjects(projects, "").size)
        assertEquals("", effectiveFilter("deleted", options))
        assertEquals("b", effectiveFilter("b", options))
        // a project whose runtime is unknown shows under All only
        assertEquals(emptyList<String>(), options.flatMap { o -> filterProjects(projects, o.id) }.filter { it.id == "3" }.map { it.id })
    }

    @Test
    fun restoredTabClampsToTabRange() {
        assertEquals(0, restoredTab(null))
        assertEquals(0, restoredTab(-1))
        assertEquals(4, restoredTab(4))
        assertEquals(0, restoredTab(WORKSPACE_TABS))
        assertEquals(2, restoredTab(2))
    }
}
