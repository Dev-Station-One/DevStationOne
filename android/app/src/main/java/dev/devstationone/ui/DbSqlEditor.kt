package dev.devstationone.ui

import android.text.InputType
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.runtime.Composable
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import io.github.rosemoe.sora.event.ContentChangeEvent
import io.github.rosemoe.sora.langs.textmate.TextMateColorScheme
import io.github.rosemoe.sora.langs.textmate.registry.ThemeRegistry
import io.github.rosemoe.sora.widget.CodeEditor

/**
 * What [SqlView] sees of the editor: current text, replace text (history pick), and whether anything
 * non-blank is typed. [pending] carries the text across an AndroidView re-creation.
 */
internal class SqlEditorHandle {
    private var editor: CodeEditor? = null
    private var pending = ""
    val hasText = mutableStateOf(false)

    val text: String get() = editor?.text?.toString() ?: pending

    fun setText(s: String) {
        pending = s
        editor?.setText(s)
        hasText.value = s.isNotBlank()
    }

    internal fun attach(e: CodeEditor) {
        editor = e
        if (pending.isNotEmpty()) e.setText(pending)
        e.subscribeEvent(ContentChangeEvent::class.java) { _, _ -> hasText.value = e.text.toString().isNotBlank() }
    }

    internal fun detach(e: CodeEditor) {
        pending = e.text.toString()
        editor = null
    }
}

/** The sora editor with the vendored SQL grammar and schema-aware completion (design D8). Fixed height so AndroidView has a definite size. */
@Composable
internal fun SqlEditor(handle: SqlEditorHandle, loader: SqlSchemaLoader, modifier: Modifier = Modifier) {
    AndroidView(
        modifier = modifier.fillMaxWidth().height(180.dp),
        factory = { c ->
            setUpTextmate(c)
            CodeEditor(c).apply {
                colorScheme = TextMateColorScheme.create(ThemeRegistry.getInstance())
                isWordwrap = false
                setTextSize(13f)
                inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_MULTI_LINE or InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS
                setEditorLanguage(SqlLanguage.create(loader))
                handle.attach(this)
            }
        },
        onRelease = { handle.detach(it); it.release() },
    )
}
