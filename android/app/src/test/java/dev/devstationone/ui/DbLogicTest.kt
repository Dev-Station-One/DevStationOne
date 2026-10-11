package dev.devstationone.ui

import dev.devstationone.net.DbConnection
import dev.devstationone.net.DbConstraint
import dev.devstationone.net.DbIndex
import dev.devstationone.net.DbBackup
import dev.devstationone.net.DbColumn
import dev.devstationone.net.DbSchedule
import dev.devstationone.net.DbGrant
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

    private val cols = listOf(
        DbColumn("id", "integer", nullable = false),
        DbColumn("name", "character varying", nullable = true),
        DbColumn("ok", "boolean", nullable = false),
        DbColumn("price", "numeric", nullable = true),
    )

    @Test
    fun coerceByColumnType() {
        assertEquals(JsonPrimitive(7L), coerceValue("integer", " 7 "))
        assertEquals(null, coerceValue("bigint", "7.5"))
        assertEquals(null, coerceValue("int", "abc"))
        assertEquals(JsonPrimitive(7L), coerceValue("numeric(10,2)", "7"))
        assertEquals(JsonPrimitive(7.5), coerceValue("numeric", "7.5"))
        assertEquals(null, coerceValue("double precision", "NaN"))
        assertEquals(JsonPrimitive(true), coerceValue("boolean", "T"))
        assertEquals(null, coerceValue("boolean", "yes"))
        assertEquals(JsonPrimitive("2026-01-01"), coerceValue("date", "2026-01-01"))
        assertEquals(JsonPrimitive("1 day"), coerceValue("interval", "1 day")) // not an int type
    }

    @Test
    fun editBlockedWithoutUsablePrimaryKey() {
        assertTrue(editBlockedReason(emptyList(), cols)!!.startsWith("No primary key"))
        assertTrue(editBlockedReason(listOf("x"), listOf(DbColumn("x", "double precision")))!!.startsWith("Floating-point"))
        assertEquals(null, editBlockedReason(listOf("id"), cols))
    }

    @Test
    fun updateSendsOnlyChangedColumns() {
        val row = mapOf<String, JsonElement>("id" to JsonPrimitive(1), "name" to JsonPrimitive("a"), "ok" to JsonPrimitive(true), "price" to JsonNull)
        val fields = mapOf(
            "name" to FieldState(FieldMode.VALUE, "b"),
            "ok" to FieldState(FieldMode.VALUE, "true"), // unchanged
            "price" to FieldState(FieldMode.NULL), // already NULL
        )
        val res = buildValues(cols, fields, row)
        assertEquals(null, res.error)
        assertEquals(mapOf<String, JsonElement>("name" to JsonPrimitive("b")), res.values)
    }

    @Test
    fun nullAndInvalidValuesAreRejected() {
        assertEquals("id is not nullable", buildValues(cols, mapOf("id" to FieldState(FieldMode.NULL)), null).error)
        assertTrue(buildValues(cols, mapOf("id" to FieldState(FieldMode.VALUE, "x")), null).error!!.startsWith("id:"))
        assertEquals(JsonNull, buildValues(cols, mapOf("name" to FieldState(FieldMode.NULL)), null).values["name"])
    }

    @Test
    fun editLandedVerifiesAgainstRefetchedPage() {
        val names = listOf("id", "name")
        val old = listOf<JsonElement>(JsonPrimitive(1), JsonPrimitive("a"))
        val changed = listOf<JsonElement>(JsonPrimitive(1), JsonPrimitive("b"))
        assertEquals(mapOf<String, JsonElement>("id" to JsonPrimitive(1)), pkOriginal(names, listOf("id"), old))
        assertTrue(editLanded("update", names, listOf("id"), old, listOf(changed)))
        assertFalse(editLanded("update", names, listOf("id"), old, listOf(old))) // zero-row update
        assertTrue(editLanded("update", names, listOf("id"), old, emptyList())) // sorted/filtered away
        assertTrue(editLanded("delete", names, listOf("id"), old, emptyList()))
        assertFalse(editLanded("delete", names, listOf("id"), old, listOf(old)))
    }

    @Test
    fun backupHelpers() {
        fun b(state: String) = DbBackup("i", state = state)
        assertTrue(anyRunning(listOf(b("done"), b("running"))))
        assertFalse(anyRunning(listOf(b("done"), b("failed"))))
        assertTrue(restoreConfirmed(" appdb ", "appdb"))
        assertFalse(restoreConfirmed("app", "appdb"))
        assertFalse(restoreConfirmed("", ""))
        assertEquals(7, parseRetention(" 7 "))
        assertEquals(null, parseRetention("0"))
        assertEquals(null, parseRetention("101"))
        assertEquals(null, parseRetention("x"))
        assertEquals("2026-10-11 04:05", whenText("2026-10-11T04:05:06Z"))
    }

    @Test
    fun backupsAndScheduleDecode() {
        val j = Json { ignoreUnknownKeys = true }
        val l = j.decodeFromString<List<DbBackup>>(
            """[{"id":"a","kind":"db","connection_id":"c","file_name":"db/a.dump","size_bytes":2048,"trigger":"manual","state":"done","error":"","created_at":"2026-10-11T04:05:06Z"}]"""
        )
        assertEquals("2 KB", fmtSize(l[0].size_bytes))
        val s = j.decodeFromString<DbSchedule>("""{"id":"s","cadence":"daily","retention":7,"next_run_at":"2026-10-12T00:00:00Z","last_run_at":null,"last_result":"","last_error":""}""")
        assertEquals(null, s.last_run_at)
        assertEquals(7, s.retention)
    }

    @Test
    fun principalAndGrantValidation() {
        assertFalse(userManagementSupported("sqlite"))
        assertTrue(userManagementSupported("postgresql"))
        assertEquals("Name is required", validatePrincipal(" ", "pw"))
        assertEquals("Password is required", validatePrincipal("bob", ""))
        assertEquals(null, validatePrincipal("bob", "pw"))
        assertEquals("Privilege is required", validateGrant("", "t"))
        assertEquals("Table is required", validateGrant("SELECT", " "))
        assertEquals(null, validateGrant("SELECT", "t"))
        assertEquals("public.t", grantTarget(DbGrant("SELECT", "public", "t")))
        assertEquals("t", grantTarget(DbGrant("SELECT", "", "t")))
    }

    @Test
    fun grantsDecodeStructuredAndRaw() {
        val l = Json { ignoreUnknownKeys = true }.decodeFromString<List<DbGrant>>(
            """[{"privilege":"SELECT","schema":"public","table":"t"},{"raw":"GRANT ALL ON *.* TO 'u'@'%'"}]"""
        )
        assertEquals("public.t", grantTarget(l[0]))
        assertEquals("GRANT ALL ON *.* TO 'u'@'%'", l[1].raw)
        assertEquals("", l[1].privilege)
    }

    @Test
    fun connectionFormValidationMatchesServerRules() {
        assertEquals(5432, defaultPort("postgresql"))
        assertEquals(0, defaultPort("sqlite"))
        assertEquals("Name is required", validateConnection("postgresql", " ", "h", "5432", "d"))
        assertEquals("Host is required", validateConnection("mysql", "n", "", "3306", "d"))
        assertEquals("Port must be 1–65535", validateConnection("mysql", "n", "h", "70000", "d"))
        assertEquals("Port must be 1–65535", validateConnection("mysql", "n", "h", "abc", "d"))
        assertEquals("Database is required", validateConnection("mssql", "n", "h", "", ""))
        assertEquals("File path is required", validateConnection("sqlite", "n", "", "", ""))
        assertEquals(null, validateConnection("sqlite", "n", "", "", "/data/x.db")) // host/port ignored
        assertEquals(null, validateConnection("postgresql", "n", "h", "", "d")) // blank port = default
    }

    @Test
    fun connectionTargetForServerAndFileEngines() {
        val pg = DbConnection("1", "p", "postgresql", host = "db", port = 5432, database = "app")
        assertEquals("db:5432/app", connectionTarget(pg))
        assertEquals("db", connectionTarget(pg.copy(port = 0, database = "")))
        assertEquals("/data/x.db", connectionTarget(DbConnection("2", "s", "sqlite", database = "/data/x.db")))
    }
}
