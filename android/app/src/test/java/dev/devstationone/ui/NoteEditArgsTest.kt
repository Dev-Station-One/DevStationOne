package dev.devstationone.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** jsString output always lands inside single quotes of an exec('cmd', 'arg') command. */
class NoteEditArgsTest {
    @Test
    fun plainArgPassesThrough() {
        assertEquals("bold", jsString("bold"))
        assertEquals("<H2>", jsString("<H2>"))
    }

    @Test
    fun hostileArgEscapesQuotesAndNewlines() {
        assertEquals("a\\'b", jsString("a'b")) // broken js string
        assertEquals("x\\ny", jsString("x\ny")) // line break out of the call
        assertEquals("back\\\\slash", jsString("back\\slash"))
        assertNoUnescapedQuote(jsString("'); Android.alert('x"))
        assertNoUnescapedQuote(jsString("</a>' onload='x"))
    }

    private fun assertNoUnescapedQuote(s: String) {
        // walk the string: a \ always pairs with the next char
        var i = 0
        while (i < s.length) {
            if (s[i] == '\\') i++
            else assertTrue("unescaped quote in \"$s\"", s[i] != '\'')
            i++
        }
    }
}