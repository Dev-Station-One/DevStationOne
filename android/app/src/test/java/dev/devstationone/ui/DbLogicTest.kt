package dev.devstationone.ui

import dev.devstationone.net.DbConnection
import dev.devstationone.net.DbConstraint
import dev.devstationone.net.DbIndex
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Test

class DbLogicTest {
    @Test
    fun primaryKeyFromConstraintThenIndexThenNone() {
        val pk = DbConstraint("t_pkey", "primary_key", listOf("a", "b"))
        val fk = DbConstraint("t_fk", "foreign_key", listOf("x"))
        val idx = DbIndex("t_pk", listOf("id"), unique = true, primary = true)
        assertEquals(listOf("a", "b"), primaryKeyColumns(listOf(fk, pk), listOf(idx)))
        assertEquals(listOf("id"), primaryKeyColumns(listOf(fk), listOf(DbIndex("u", listOf("z")), idx)))
        assertEquals(emptyList<String>(), primaryKeyColumns(listOf(fk), listOf(DbIndex("u", listOf("z")))))
    }

    @Test
    fun defaultSchemaPerEngine() {
        assertEquals("", defaultSchema("sqlite", listOf("main")))
        assertFalse(hasSchemas("sqlite"))
        assertEquals("public", defaultSchema("postgresql", listOf("audit", "public")))
        assertEquals("audit", defaultSchema("postgresql", listOf("audit")))
        assertEquals("app", defaultSchema("mysql", listOf("app", "public")))
        assertEquals("", defaultSchema("mssql", emptyList()))
    }

    @Test
    fun errorTextUnwrapsServerJsonAndCaps() {
        assertEquals("runtime offline", dbErrorText("""{"error":"runtime offline"}"""))
        assertEquals("HTTP 504", dbErrorText("HTTP 504"))
        assertEquals("", dbErrorText(null))
        assertEquals(200, dbErrorText("x".repeat(500)).length)
    }

    @Test
    fun connectionTargetForServerAndFileEngines() {
        val pg = DbConnection("1", "p", "postgresql", host = "db", port = 5432, database = "app")
        assertEquals("db:5432/app", connectionTarget(pg))
        assertEquals("db", connectionTarget(pg.copy(port = 0, database = "")))
        assertEquals("/data/x.db", connectionTarget(DbConnection("2", "s", "sqlite", database = "/data/x.db")))
    }
}
