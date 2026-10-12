import com.github.jengelman.gradle.plugins.shadow.tasks.ShadowJar

plugins {
    java
    application
    id("com.github.johnrengelman.shadow") version "8.1.1"
}

group = "com.batnam.e2e"
version = "1.0.0"

java {
    toolchain {
        languageVersion.set(JavaLanguageVersion.of(21))
    }
}

repositories {
    maven { url = uri("/candidate/repository") }
    mavenCentral()
}

dependencies {
    implementation("io.github.batnam:workflow-sdk-java:${providers.gradleProperty("sdkVersion").get()}")
}

application {
    mainClass.set("com.batnam.e2e.Worker")
}

tasks.named<ShadowJar>("shadowJar") {
    archiveFileName.set("worker-all.jar")
    mergeServiceFiles()
}
