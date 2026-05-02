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
    mavenCentral()
}

dependencies {
    implementation("com.batnam.rochallor-engine:workflow-sdk-java:1.0.0")
    implementation("io.grpc:grpc-netty-shaded:1.71.0")
    implementation("io.grpc:grpc-stub:1.71.0")
    implementation("io.grpc:grpc-protobuf:1.71.0")
}

application {
    mainClass.set("com.batnam.e2e.Worker")
}

tasks.named<ShadowJar>("shadowJar") {
    archiveFileName.set("worker-all.jar")
    mergeServiceFiles()
}
