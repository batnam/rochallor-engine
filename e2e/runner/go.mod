module github.com/batnam/e2e/runner

go 1.26.2

require (
	github.com/batnam/rochallor-engine/workflow-engine v0.0.0
	google.golang.org/grpc v1.79.3
	google.golang.org/protobuf v1.36.11
)

require (
	golang.org/x/net v0.49.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260406210006-6f92a3bedf2d // indirect
)

replace (
	github.com/batnam/rochallor-engine/workflow-engine => ../../workflow-engine
	github.com/batnam/rochallor-engine/workflow-sdk-go => ../../workflow-sdk-go
)
