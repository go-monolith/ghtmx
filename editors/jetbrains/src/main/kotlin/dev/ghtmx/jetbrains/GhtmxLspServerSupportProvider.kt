package dev.ghtmx.jetbrains

import com.intellij.execution.configurations.GeneralCommandLine
import com.intellij.openapi.project.Project
import com.intellij.openapi.vfs.VirtualFile
import com.intellij.platform.lsp.api.LspServerSupportProvider
import com.intellij.platform.lsp.api.ProjectWideLspServerDescriptor

// Launches `ghtmx lsp` for ghtmx files. The server provides diagnostics,
// completion, hover, and go to definition, and proxies gopls for the
// embedded Go, so the plugin stays a thin client.
class GhtmxLspServerSupportProvider : LspServerSupportProvider {
    override fun fileOpened(
        project: Project,
        file: VirtualFile,
        serverStarter: LspServerSupportProvider.LspServerStarter,
    ) {
        if (file.extension == "ghtmx") {
            serverStarter.ensureServerStarted(GhtmxLspServerDescriptor(project))
        }
    }
}

private class GhtmxLspServerDescriptor(project: Project) :
    ProjectWideLspServerDescriptor(project, "ghtmx") {
    override fun isSupportedFile(file: VirtualFile): Boolean = file.extension == "ghtmx"

    override fun createCommandLine(): GeneralCommandLine = GeneralCommandLine("ghtmx", "lsp")
}
