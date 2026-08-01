package dev.ghtmx.jetbrains

import com.intellij.openapi.application.PathManager
import org.jetbrains.plugins.textmate.api.TextMateBundleProvider
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.StandardCopyOption

// Serves the shared ghtmx TextMate bundle (a copy of the VS Code
// grammar, kept byte-identical by the repository's editors test) so
// highlighting matches across editors.
class GhtmxTextMateBundleProvider : TextMateBundleProvider {
    override fun getBundles(): List<TextMateBundleProvider.PluginBundle> {
        val dir: Path = PathManager.getTempPath().let { Path.of(it, "ghtmx-textmate") }
        Files.createDirectories(dir)
        for (name in listOf("package.json", "ghtmx.tmLanguage.json")) {
            javaClass.getResourceAsStream("/textmate/$name").use { stream ->
                if (stream != null) {
                    Files.copy(stream, dir.resolve(name), StandardCopyOption.REPLACE_EXISTING)
                }
            }
        }
        return listOf(TextMateBundleProvider.PluginBundle("ghtmx", dir))
    }
}
