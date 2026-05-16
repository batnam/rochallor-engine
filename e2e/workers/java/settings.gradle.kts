rootProject.name = "e2e-worker-java"

includeBuild("/sdk") {
    dependencySubstitution {
        substitute(module("io.github.batnamv:workflow-sdk-java")).using(project(":"))
    }
}
