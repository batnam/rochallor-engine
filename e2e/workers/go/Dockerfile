FROM golang:1.26-alpine AS build
WORKDIR /src

# Copy the SDK source for local replace
COPY workflow-sdk-go /src/workflow-sdk-go

# Stage the worker module + source together so go mod tidy sees all imports
COPY e2e/workers/go/go.mod /src/worker/go.mod
COPY e2e/workers/go/main.go /src/worker/main.go
WORKDIR /src/worker

RUN go mod edit -replace github.com/batnam/rochallor-engine/workflow-sdk-go=/src/workflow-sdk-go
RUN go mod tidy
RUN CGO_ENABLED=0 go build -o /worker-bin .

FROM alpine:3.20
COPY --from=build /worker-bin /worker
ENTRYPOINT ["/worker"]
