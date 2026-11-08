plugins {
    id("com.android.application") version "9.5.1" apply false
    // 2.3.10: sora-editor 0.24.6 ships Kotlin 2.x-metadata modules (parity stage 5)
    id("org.jetbrains.kotlin.plugin.compose") version "2.3.10" apply false
    id("org.jetbrains.kotlin.plugin.serialization") version "2.3.10" apply false
}