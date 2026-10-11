package dev.devstationone.ui

import dev.devstationone.net.DbConnection
import dev.devstationone.net.DbConstraint
import dev.devstationone.net.DbIndex
import dev.devstationone.net.DbRows
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
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
    fun sortCyclesAscDescNoneAndResetsOnNewColumn() {
        val a = nextSort(DbSort(), "id")
        assertEquals(DbSort("id", false), a)
        val d = nextSort(a, "id")
        assertEquals(DbSort("id", true), d)
        assertEquals(DbSort(), nextSort(d, "id"))
        assertEquals(DbSort("name", false), nextSort(d, "name"))
    }

    @Test
    fun pageLabelAndCellText() {
        assertEquals("Rows 101–200", pageLabel(100, 100))
        assertEquals("No rows", pageLabel(0, 0))
        assertEquals("NULL", cellText(JsonNull))
        assertEquals("hi", cellText(JsonPrimitive("hi")))
        assertEquals("42", cellText(JsonPrimitive(42)))
        assertEquals("true", cellText(JsonPrimitive(true)))
    }

    @Test
    fun columnWidthClamps() {
        val rows = listOf(listOf<JsonElement>(JsonPrimitive("x".repeat(500))))
        assertEquals(200, columnWidthDp("a", rows, 0))
        assertEquals(72, columnWidthDp("id", listOf(listOf(JsonPrimitive(1))), 0))
    }

    @Test
    fun rowsDecodeKeepsNullsAndMixedTypes() {
        val r = Json { ignoreUnknownKeys = true }.decodeFromString<DbRows>(
            """{"columns":["id","n","ok"],"rows":[[1,null,true],[2,"a",false]],"has_more":true,"is_exec":false,"rows_affected":0}"""
        )
        assertEquals(listOf("1", "NULL", "true"), r.rows[0].map(::cellText))
        assertEquals("a", cellText(r.rows[1][1]))
        assertEquals(true, r.has_more)
    }

    @Test
    fun classifierReadOnly() {
        listOf(
            "select 1", "  SELECT * FROM t;", "-- c\nSELECT 1", "/* c */ with x as (select 1) select * from x",
            "EXPLAIN SELECT 1", "show tables", "PRAGMA table_info(t)", "SELECT 'a--b' FROM t", "SELECT 1; ; ",
        ).forEach { assertTrue(it, isReadOnlySql(it)) }
    }

    @Test
    fun classifierWritesAndMultiStatement() {
        listOf(
            "DELETE FROM t", "update t set a=1", "DROP TABLE t", "INSERT INTO t VALUES (1)",
            "SELECT 1; DROP TABLE t", "SELECT 1 -- x\n; DELETE FROM t", "/* SELECT */ DELETE FROM t",
            "SELECT '--'; DROP TABLE t", "(SELECT 1)", "", "   ",
        ).forEach { assertFalse(it, isReadOnlySql(it)) }
    }

    @Test
    fun historyDedupesCapsAndOrders() {
        assertEquals(listOf("b", "a"), pushHistory(listOf("a", "b"), " b "))
        val full = (1..SQL_HISTORY_MAX).map { "q$it" }
        val next = pushHistory(full, "new")
        assertEquals(SQL_HISTORY_MAX, next.size)
        assertEquals("new", next.first())
        assertFalse("q$SQL_HISTORY_MAX" in next)
    }

    @Test
    fun connectionTargetForServerAndFileEngines() {
        val pg = DbConnection("1", "p", "postgresql", host = "db", port = 5432, database = "app")
        assertEquals("db:5432/app", connectionTarget(pg))
        assertEquals("db", connectionTarget(pg.copy(port = 0, database = "")))
        assertEquals("/data/x.db", connectionTarget(DbConnection("2", "s", "sqlite", database = "/data/x.db")))
    }
}
