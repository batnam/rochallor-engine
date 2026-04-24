FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /engine ./cmd/engine

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget
COPY --from=build /engine /engine
ENTRYPOINT ["/engine"]
