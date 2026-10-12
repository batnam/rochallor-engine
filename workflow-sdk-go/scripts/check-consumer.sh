#!/usr/bin/env sh
set -eu
SDK_DIR="$(cd "$(dirname "$0")/.." && pwd)"
CONSUMER_DIR="$(mktemp -d)"
trap 'rm -rf "$CONSUMER_DIR"' EXIT
cd "$CONSUMER_DIR"
go mod init example.com/rochallor-sdk-consumer
go mod edit -require=github.com/batnam/rochallor-engine/workflow-sdk-go@v0.0.0
go mod edit "-replace=github.com/batnam/rochallor-engine/workflow-sdk-go=$SDK_DIR"
cat > main.go <<'GO'
package main

import (
    "context"
    "github.com/batnam/rochallor-engine/workflow-sdk-go/client"
    "github.com/batnam/rochallor-engine/workflow-sdk-go/handler"
    "github.com/batnam/rochallor-engine/workflow-sdk-go/kafkarunner"
    "github.com/batnam/rochallor-engine/workflow-sdk-go/runner"
)

// A pre-heartbeat custom client still satisfies EngineClient unchanged.
type customClient struct{}
func(customClient)PollJobs(context.Context,client.PollJobsRequest)([]client.Job,error){return nil,nil}
func(customClient)CompleteJob(context.Context,string,string,map[string]any)error{return nil}
func(customClient)FailJob(context.Context,string,string,string,bool)error{return nil}
var _ client.EngineClient=customClient{}

func main() {
    rest := client.NewRest("http://127.0.0.1:1", "consumer")
    grpc, err := client.NewGrpc("127.0.0.1:1", "consumer")
    if err != nil { panic(err) }
    defer grpc.Close()
    registry := handler.New()
    registry.Register("smoke", func(context.Context, handler.JobContext) (handler.Result, error) { return handler.Result{}, nil })
    for _, engine := range []client.EngineClient{rest, grpc, customClient{}} {
        ctx, cancel := context.WithCancel(context.Background())
        cancel()
        runner.New(runner.Config{WorkerID: "consumer"}, engine, registry).Run(ctx)
        if _, err := kafkarunner.New(kafkarunner.Config{WorkerID: "consumer", SeedBrokers: "127.0.0.1:1", JobTypes: registry.JobTypes()}, engine, registry); err != nil { panic(err) }
    }
}
GO
go mod tidy
go run .
echo "External Go module consumer passed for source $(git -C "$SDK_DIR" rev-parse HEAD)"
