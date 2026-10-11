package dev.devstationone.ui

import io.github.rosemoe.sora.langs.textmate.registry.FileProviderRegistry
import io.github.rosemoe.sora.langs.textmate.registry.GrammarRegistry
import io.github.rosemoe.sora.langs.textmate.registry.ThemeRegistry
import io.github.rosemoe.sora.langs.textmate.registry.model.ThemeModel
import io.github.rosemoe.sora.langs.textmate.registry.provider.AssetsFileResolver
import org.eclipse.tm4e.core.registry.IThemeSource

// TextMate registries are process-wide statics; one-time setup, guarded. Shared by the file editor and the SQL editor.
@Volatile private var textmateReady = false
private val textmateLock = Any()

internal fun setUpTextmate(ctx: android.content.Context) {
    if (textmateReady) return
    synchronized(textmateLock) {
        if (textmateReady) return
        FileProviderRegistry.getInstance().addFileProvider(AssetsFileResolver(ctx.assets))
        ThemeRegistry.getInstance().loadTheme(
            ThemeModel(
                IThemeSource.fromInputStream(
                    FileProviderRegistry.getInstance().tryGetInputStream("textmate/darcula.json"),
                    "textmate/darcula.json", null,
                ),
                "darcula",
            ).apply { isDark = true }
        )
        GrammarRegistry.getInstance().loadGrammars("textmate/languages.json")
        textmateReady = true
    }
}
