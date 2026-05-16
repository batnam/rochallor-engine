import com.google.protobuf.gradle.*
import com.vanniktech.maven.publish.SonatypeHost

plugins {
    java
    `java-library`
    id("com.google.protobuf") version "0.9.4"
    id("checkstyle")
    id("com.vanniktech.maven.publish") version "0.30.0"
}

group = "com.batnam"
version = "1.0.0"

java {
    toolchain {
        languageVersion.set(JavaLanguageVersion.of(21))
    }
}

// ──────────────────────────────────────────────────────────────────────────────
// Dependency versions
// ──────────────────────────────────────────────────────────────────────────────
val grpcVersion = "1.71.0"
val protobufVersion = "4.30.2"
val jacksonVersion = "2.18.3"
val micrometerVersion = "1.14.5"
val junitVersion = "5.11.4"
val assertjVersion = "3.27.3"
val mockitoVersion = "5.15.2"

// ──────────────────────────────────────────────────────────────────────────────
// Banned coordinates — fail the build if any of these appear transitively
// ──────────────────────────────────────────────────────────────────────────────
configurations.all {
    resolutionStrategy {
        eachDependency {
            val banned = listOf(
                "org.flowable",
                "org.camunda",
                "io.camunda",
                "io.zeebe",
                "org.springframework",
                "org.springframework.boot"
            )
            if (banned.any { requested.group.startsWith(it) }) {
                throw GradleException(
                    "Banned dependency: ${requested.group}:${requested.name}. " +
                    "This SDK must not import Flowable, Camunda, Zeebe, or Spring."
                )
            }
        }
    }
}

repositories {
    mavenCentral()
}

dependencies {
    // gRPC
    implementation("io.grpc:grpc-netty-shaded:$grpcVersion")
    implementation("io.grpc:grpc-protobuf:$grpcVersion")
    implementation("io.grpc:grpc-stub:$grpcVersion")
    compileOnly("org.apache.tomcat:annotations-api:6.0.53") // javax.annotation for generated stubs

    // Protobuf
    implementation("com.google.protobuf:protobuf-java:3.25.3")
    implementation("org.apache.kafka:kafka-clients:3.7.0")
    implementation("com.google.protobuf:protobuf-java-util:$protobufVersion")

    // JSON
    implementation("com.fasterxml.jackson.core:jackson-databind:$jacksonVersion")
    implementation("com.fasterxml.jackson.datatype:jackson-datatype-jsr310:$jacksonVersion")

    // Metrics
    implementation("io.micrometer:micrometer-core:$micrometerVersion")

    // Test
    testImplementation(platform("org.junit:junit-bom:$junitVersion"))
    testImplementation("org.junit.jupiter:junit-jupiter")
    testImplementation("org.assertj:assertj-core:$assertjVersion")
    testImplementation("org.mockito:mockito-core:$mockitoVersion")
    testImplementation("org.mockito:mockito-junit-jupiter:$mockitoVersion")
    testRuntimeOnly("org.junit.platform:junit-platform-launcher")
    testImplementation("io.grpc:grpc-inprocess:$grpcVersion")
    testImplementation("com.github.tomakehurst:wiremock-jre8:2.35.2")
}

// ──────────────────────────────────────────────────────────────────────────────
// Protobuf / gRPC code generation
// ──────────────────────────────────────────────────────────────────────────────
protobuf {
    protoc {
        artifact = "com.google.protobuf:protoc:$protobufVersion"
    }
    plugins {
        id("grpc") {
            artifact = "io.grpc:protoc-gen-grpc-java:$grpcVersion"
        }
    }
    generateProtoTasks {
        all().forEach {
            it.plugins {
                id("grpc")
            }
        }
    }
}

// Point protoc at the shared proto directory
sourceSets {
    main {
        proto {
            srcDir("../proto")
        }
        java {
            srcDir("build/generated/source/proto/main/java")
            srcDir("build/generated/source/proto/main/grpc")
        }
    }
}

// Avoid duplicate-entry errors in sourcesJar/javadocJar because the protobuf
// plugin auto-registers the generated source directories that we also list above
tasks.withType<Jar>().configureEach {
    duplicatesStrategy = DuplicatesStrategy.EXCLUDE
}

// ──────────────────────────────────────────────────────────────────────────────
// Checkstyle
// ──────────────────────────────────────────────────────────────────────────────
checkstyle {
    toolVersion = "10.21.1"
    configFile = file("config/checkstyle/checkstyle.xml")
}

tasks.withType<Checkstyle> {
    enabled = false
}

// ──────────────────────────────────────────────────────────────────────────────
// Tests
// ──────────────────────────────────────────────────────────────────────────────
tasks.test {
    useJUnitPlatform()
}

// ──────────────────────────────────────────────────────────────────────────────
// Publishing — Maven Central via Sonatype Central Portal
// ──────────────────────────────────────────────────────────────────────────────
mavenPublishing {
    publishToMavenCentral(SonatypeHost.CENTRAL_PORTAL)
    signAllPublications()

    coordinates("com.batnam", "workflow-sdk-java", version.toString())

    pom {
        name.set("workflow-sdk-java")
        description.set("Java SDK for the Rochallor workflow engine")
        url.set("https://github.com/batnam/rochallor-engine")
        licenses {
            license {
                name.set("Apache License, Version 2.0")
                url.set("https://www.apache.org/licenses/LICENSE-2.0")
            }
        }
        developers {
            developer {
                id.set("batnam")
                name.set("batnam")
                url.set("https://github.com/batnam")
            }
        }
        scm {
            url.set("https://github.com/batnam/rochallor-engine")
            connection.set("scm:git:git://github.com/batnam/rochallor-engine.git")
            developerConnection.set("scm:git:ssh://git@github.com/batnam/rochallor-engine.git")
        }
    }
}
