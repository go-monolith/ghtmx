package dev.ghtmx.jetbrains

import com.intellij.execution.configurations.GeneralCommandLine
import com.intellij.openapi.project.Project
import com.intellij.openapi.vfs.VirtualFile
import com.intellij.platform.lsp.api.LspServerSupportProvider
import com.intellij.platform.lsp.api.ProjectWideLspServerDescriptor

// The template extensions this plugin claims. `.ghtmx` is canonical;
// `.htmx` is accepted because some projects prefer it.
internal val GHTMX_EXTENSIONS = setOf("ghtmx", "htmx")

// Launches `ghtmx lsp` for ghtmx files. The server provides diagnostics,
// completion, hover, and go to definition, and proxies gopls for the
// embedded Go, so the plugin stays a thin client.
class GhtmxLspServerSupportProvider : LspServerSupportProvider {
    override fun fileOpened(
        project: Project,
        file: VirtualFile,
        serverStarter: LspServerSupportProvider.LspServerStarter,
    ) {
        if (file.extension in GHTMX_EXTENSIONS) {
            serverStarter.ensureServerStarted(GhtmxLspServerDescriptor(project))
        }
    }
}

private class GhtmxLspServerDescriptor(project: Project) :
    ProjectWideLspServerDescriptor(project, "ghtmx") {
    override fun isSupportedFile(file: VirtualFile): Boolean = file.extension in GHTMX_EXTENSIONS

    override fun createCommandLine(): GeneralCommandLine = GeneralCommandLine("ghtmx", "lsp")
}
