plugins {
    id("java")
    id("org.jetbrains.kotlin.jvm") version "2.0.21"
    id("org.jetbrains.intellij.platform") version "2.1.0"
}

group = "dev.ghtmx"
version = "0.1.0"

repositories {
    mavenCentral()
    intellijPlatform {
        defaultRepositories()
        // Hosts the Java compiler tooling instrumentCode runs;
        // defaultRepositories() does not cover it.
        intellijDependencies()
    }
}

dependencies {
    intellijPlatform {
        // The native LSP client API ships with the commercial IDEs only
        // (2023.2+); Community users can wire the server via LSP4IJ
        // instead — see README.md.
        intellijIdeaUltimate("2024.2")
        bundledPlugin("org.jetbrains.plugins.textmate")
        // buildPlugin runs instrumentCode, which needs a Java compiler
        // dependency; without this the task fails with "No Java Compiler
        // dependency found" and no artifact is produced.
        instrumentationTools()
    }
}

intellijPlatform {
    pluginConfiguration {
        id = "dev.ghtmx.jetbrains"
        name = "ghtmx"
        ideaVersion {
            sinceBuild = "242"
        }
    }
    publishing {
        token = providers.environmentVariable("PUBLISH_TOKEN")
    }
}

kotlin {
    jvmToolchain(17)
}
