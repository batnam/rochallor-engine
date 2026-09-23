module github.com/batnam/e2e/runner

go 1.27.1

require (
	github.com/batnam/rochallor-engine/workflow-engine v0.0.0
	github.com/twmb/franz-go v1.20.7
	google.golang.org/grpc v1.79.3
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/klauspost/compress v1.18.5 // indirect
	github.com/pierrec/lz4/v4 v4.1.25 // indirect
	github.com/twmb/franz-go/pkg/kmsg v1.12.0 // indirect
	golang.org/x/net v0.49.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260406210006-6f92a3bedf2d // indirect
)

replace (
	github.com/batnam/rochallor-engine/workflow-engine => ../../workflow-engine
	github.com/batnam/rochallor-engine/workflow-sdk-go => ../../workflow-sdk-go
)
