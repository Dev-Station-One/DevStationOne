package dev.devstationone.ui

import dev.devstationone.net.ProjectInfo
import dev.devstationone.net.RuntimeInfo

// Pure helpers for the project list and workspace tab memory (docs/DESIGN_ANDROID_ROUND4.md D2/D7).

/** Number of workspace tabs; the tab row and the stored-index range check share it. */
internal const val WORKSPACE_TABS = 5

/** Stored tab index → tab to open; anything missing or out of range is Terminal. */
internal fun restoredTab(stored: Int?): Int = stored?.takeIf { it in 0 until WORKSPACE_TABS } ?: 0

internal fun sortedProjects(list: List<ProjectInfo>): List<ProjectInfo> =
    list.sortedWith(compareBy<ProjectInfo, String>(String.CASE_INSENSITIVE_ORDER) { it.name }.thenBy { it.id })

/** Only runtimes that own at least one project, by name. */
internal fun runtimeOptions(projects: List<ProjectInfo>, runtimes: List<RuntimeInfo>): List<RuntimeInfo> =
    runtimes.filter { r -> projects.any { it.runtime_id == r.id } }
        .sortedWith(compareBy<RuntimeInfo, String>(String.CASE_INSENSITIVE_ORDER) { it.name }.thenBy { it.id })

/** A stored runtime id that is no longer an option behaves as All (""); the stored value is left alone. */
internal fun effectiveFilter(stored: String, options: List<RuntimeInfo>): String =
    stored.takeIf { s -> options.any { it.id == s } }.orEmpty()

internal fun filterProjects(list: List<ProjectInfo>, runtimeID: String): List<ProjectInfo> =
    if (runtimeID.isEmpty()) list else list.filter { it.runtime_id == runtimeID }
