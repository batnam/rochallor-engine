rootProject.name = "e2e-worker-java"

includeBuild("/sdk") {
    dependencySubstitution {
        substitute(module("com.batnam.rochallor-engine:workflow-sdk-java")).using(project(":"))
    }
}
