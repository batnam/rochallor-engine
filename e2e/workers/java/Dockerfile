FROM gradle:8.13-jdk21 AS build
WORKDIR /build
COPY workflow-sdk-java/artifacts/candidate /candidate
RUN cd /candidate && sha256sum -c SHA256SUMS
COPY e2e/workers/java/build.gradle.kts e2e/workers/java/settings.gradle.kts ./
COPY e2e/workers/java/src ./src
RUN gradle shadowJar --no-daemon -PsdkVersion="$(cat /candidate/version.txt)"

FROM eclipse-temurin:21-jre
COPY --from=build /build/build/libs/worker-all.jar /worker.jar
ENTRYPOINT ["java", "-jar", "/worker.jar"]
