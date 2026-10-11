package dev.devstationone.ui

import dev.devstationone.net.DbColumn
import dev.devstationone.net.DbTable
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class DbCompletionTest {
    private val schema = SqlSchema(
        tables = listOf(DbTable("orders"), DbTable("order_items"), DbTable("users"), DbTable("Mixed Case", "view")),
        columns = mapOf(
            "orders" to listOf(DbColumn("id", "int"), DbColumn("user_id", "int"), DbColumn("total", "numeric")),
            "users" to listOf(DbColumn("id", "int"), DbColumn("name", "text")),
        ),
    )

    private fun at(sql: String) = completionAt(sql, sql.length)

    @Test
    fun completionAtPrefixAndQualifier() {
        assertEquals("sel", at("sel")!!.prefix)
        assertEquals("or", at("SELECT * FROM or")!!.prefix)
        val q = at("SELECT orders.to")!!
        assertEquals("to", q.prefix)
        assertEquals("orders", q.qualifier)
        assertEquals("orders", at("SELECT \"orders\".")!!.qualifier)
        assertEquals("", at("SELECT `orders`.")!!.prefix)
        assertEquals("orders", at("SELECT [orders].t")!!.qualifier)
    }

    @Test
    fun completionAtNothingToComplete() {
        assertNull(at(""))
        assertNull(at("SELECT * FROM "))
        assertNull(at("SELECT 1, "))
    }

    @Test
    fun completionAtNotesAnOpeningQuote() {
        val c = at("SELECT \"or")!!
        assertEquals("or", c.prefix)
        assertTrue(c.quoted)
        assertTrue(!at("SELECT or")!!.quoted)
    }

    @Test
    fun completionAtMidText() {
        val sql = "SELECT o.i FROM orders o"
        val c = completionAt(sql, "SELECT o.i".length)!!
        assertEquals("i", c.prefix)
        assertEquals("o", c.qualifier)
    }

    @Test
    fun aliasMapResolvesSimpleAliases() {
        val m = aliasMap("SELECT * FROM orders o JOIN public.users AS u ON u.id = o.user_id WHERE o.id > 1")
        assertEquals("orders", m["o"])
        assertEquals("users", m["u"])
        assertEquals(2, m.size)
    }

    @Test
    fun aliasMapSkipsReservedWordsAndSubqueries() {
        assertTrue(aliasMap("SELECT * FROM orders WHERE id = 1").isEmpty())
        assertTrue(aliasMap("SELECT * FROM orders ORDER BY id").isEmpty())
        assertTrue(aliasMap("SELECT * FROM (SELECT 1) x").isEmpty())
        assertEquals("orders", aliasMap("select * from \"orders\" o")["o"])
    }

    @Test
    fun suggestKeywordsThenTablesThenColumns() {
        val s = suggest(at("o")!!, schema, "postgresql", emptyMap())
        val kinds = s.map { it.kind }
        assertEquals(kinds.sortedBy { it.ordinal }, kinds) // grouped in that order
        assertTrue(s.any { it.text == "ORDER" && it.kind == SuggestKind.KEYWORD })
        assertEquals(listOf("order_items", "orders"), s.filter { it.kind == SuggestKind.TABLE }.map { it.text })
    }

    @Test
    fun suggestMatchesCaseInsensitivelyAndUppercasesKeywords() {
        assertTrue(suggest(at("SEL")!!, schema, "sqlite", emptyMap()).any { it.text == "SELECT" })
        assertTrue(suggest(at("sel")!!, schema, "sqlite", emptyMap()).any { it.text == "SELECT" })
        assertEquals(listOf("users"), suggest(at("USERS")!!, schema, "sqlite", emptyMap()).filter { it.kind == SuggestKind.TABLE }.map { it.text })
    }

    @Test
    fun suggestColumnsAfterQualifierOrAlias() {
        assertEquals(listOf("id", "name"), suggest(at("SELECT users.")!!, schema, "postgresql", emptyMap()).map { it.text })
        assertEquals(listOf("total"), suggest(at("SELECT o.t")!!, schema, "postgresql", mapOf("o" to "orders")).map { it.text })
        assertEquals(listOf("id"), suggest(at("SELECT USERS.i")!!, schema, "postgresql", emptyMap()).map { it.text })
    }

    @Test
    fun suggestUnknownQualifierOffersTablesAndLoadedColumnsDetailNamesOwner() {
        assertEquals(listOf("orders"), suggest(at("FROM public.orde")!!, schema, "postgresql", emptyMap()).filter { it.text == "orders" }.map { it.text })
        val ids = suggest(at("id")!!, schema, "postgresql", emptyMap()).filter { it.kind == SuggestKind.COLUMN }
        assertEquals(listOf("orders", "users"), ids.map { it.detail })
    }

    @Test
    fun suggestIsCapped() {
        val many = SqlSchema(tables = (1..200).map { DbTable("t$it") })
        assertEquals(MAX_SUGGESTIONS, suggest(at("t")!!, many, "mysql", emptyMap()).size)
    }

    @Test
    fun suggestWithoutSchemaStillOffersKeywords() {
        val s = suggest(at("sel")!!, SqlSchema(), "mssql", emptyMap())
        assertNotNull(s.firstOrNull { it.text == "SELECT" })
        assertTrue(s.all { it.kind == SuggestKind.KEYWORD })
    }

    @Test
    fun engineKeywordExtras() {
        assertTrue("ILIKE" in keywordsFor("postgresql"))
        assertTrue("ILIKE" !in keywordsFor("mysql"))
        assertTrue("TOP" in keywordsFor("mssql"))
        assertTrue("PRAGMA" in keywordsFor("sqlite"))
        assertTrue("DESCRIBE" in keywordsFor("mariadb"))
    }

    @Test
    fun insertTextQuotesOnlyWhenNeeded() {
        assertEquals("orders", insertText("postgresql", "orders"))
        assertEquals("\"Orders\"", insertText("postgresql", "Orders"))
        assertEquals("\"mixed case\"", insertText("sqlite", "mixed case"))
        assertEquals("`Mixed Case`", insertText("mysql", "Mixed Case"))
        assertEquals("`order`", insertText("mariadb", "order")) // reserved word
        assertEquals("[Mixed Case]", insertText("mssql", "Mixed Case"))
        assertEquals("\"a\"\"b\"", insertText("postgresql", "a\"b"))
        assertEquals("[a]]b]", insertText("mssql", "a]b"))
    }
}
