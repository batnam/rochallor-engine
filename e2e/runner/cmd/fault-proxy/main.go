package main

import (
	"log"
	"net"
	"net/http"
	"os"

	"github.com/batnam/e2e/runner/faultproxy"
	workflowv1 "github.com/batnam/rochallor-engine/workflow-engine/api/gen/workflow/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	conn, err := grpc.NewClient(os.Getenv("ENGINE_GRPC_HOST"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	proxy, err := faultproxy.New(os.Getenv("ENGINE_REST_URL"), workflowv1.NewWorkflowEngineClient(conn))
	if err != nil {
		log.Fatal(err)
	}
	listener, err := net.Listen("tcp", ":9093")
	if err != nil {
		log.Fatal(err)
	}
	server := grpc.NewServer()
	workflowv1.RegisterWorkflowEngineServer(server, proxy)
	go func() { log.Fatal(server.Serve(listener)) }()
	log.Fatal(http.ListenAndServe(":8081", proxy))
}
