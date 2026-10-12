plugins { java; application }
java { toolchain { languageVersion.set(JavaLanguageVersion.of(21)) } }
repositories {
    maven { url = uri(providers.gradleProperty("candidateRepository").get()) }
    mavenCentral()
}
dependencies { implementation("io.github.batnam:workflow-sdk-java:${providers.gradleProperty("sdkVersion").get()}") }
application { mainClass.set("ConsumerSmoke") }
// Exercise renewal while the only virtual-thread carrier is busy in a handler.
tasks.named<JavaExec>("run") {
    jvmArgs("-Djdk.virtualThreadScheduler.parallelism=1", "-Djdk.virtualThreadScheduler.maxPoolSize=1")
}
