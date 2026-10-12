FROM golang:1.27-alpine AS build
WORKDIR /src/e2e/runner
COPY e2e/runner ./
COPY workflow-engine /src/workflow-engine
RUN CGO_ENABLED=0 go build -o /fault-proxy ./cmd/fault-proxy

FROM alpine:3.21
COPY --from=build /fault-proxy /fault-proxy
ENTRYPOINT ["/fault-proxy"]
