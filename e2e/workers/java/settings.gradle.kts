rootProject.name = "e2e-worker-java"

includeBuild("/sdk") {
    dependencySubstitution {
        substitute(module("io.github.batnam:workflow-sdk-java")).using(project(":"))
    }
}
