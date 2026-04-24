FROM gradle:8.13-jdk21 AS build
WORKDIR /build

# Copy SDK source for composite build substitution
COPY workflow-sdk-java /sdk
COPY proto /proto

# Copy worker build files
COPY e2e/workers/java/build.gradle.kts e2e/workers/java/settings.gradle.kts ./
COPY e2e/workers/java/src ./src

RUN gradle shadowJar --no-daemon

FROM eclipse-temurin:21-jre
COPY --from=build /build/build/libs/worker-all.jar /worker.jar
ENTRYPOINT ["java", "-jar", "/worker.jar"]
