package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/batnam/rochallor-engine/workflow-sdk-go/client"
	workflowv1 "github.com/batnam/rochallor-engine/workflow-sdk-go/internal/gen/workflow/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestRESTHeartbeatMapping(t *testing.T) {
	for _, tc := range []struct {
		code     int
		expected error
	}{{200, nil}, {404, client.ErrHeartbeatUnsupported}, {405, client.ErrHeartbeatUnsupported}, {501, client.ErrHeartbeatUnsupported}, {409, client.ErrLeaseLost}, {503, nil}} {
		expiry := time.Now().Add(time.Minute).UTC().Truncate(time.Microsecond)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				WorkerID     string `json:"workerId"`
				LeaseSeconds int    `json:"leaseSeconds"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if r.URL.Path != "/v1/jobs/job/heartbeat" || body.WorkerID != "owner" || body.LeaseSeconds != 60 {
				t.Error("wrong heartbeat request")
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(tc.code)
			if tc.code == 200 {
				json.NewEncoder(w).Encode(map[string]any{"accepted": true, "lockExpiresAt": expiry})
			}
		}))
		defer server.Close()
		got, err := client.NewRest(server.URL, "owner").ExtendJobLease(t.Context(), "job", "owner", 60)
		if tc.code == 200 {
			if err != nil || !got.Equal(expiry) {
				t.Fatalf("expiry=%s err=%v", got, err)
			}
		} else if tc.expected != nil {
			if !errors.Is(err, tc.expected) {
				t.Fatalf("status=%d error=%v", tc.code, err)
			}
		} else {
			var protocolError *client.HTTPError
			if !errors.As(err, &protocolError) || protocolError.StatusCode != 503 {
				t.Fatalf("transport error lost: %v", err)
			}
		}
	}
}

type heartbeatServer struct {
	workflowv1.UnimplementedWorkflowEngineServer
	code   codes.Code
	expiry time.Time
}

func (s *heartbeatServer) ExtendJobLease(_ context.Context, req *workflowv1.ExtendJobLeaseRequest) (*workflowv1.ExtendJobLeaseResponse, error) {
	if req.JobId != "job" || req.WorkerId != "owner" || req.LeaseSeconds != 60 {
		return nil, status.Error(codes.InvalidArgument, "wrong request")
	}
	if s.code != codes.OK {
		return nil, status.Error(s.code, "injected")
	}
	return &workflowv1.ExtendJobLeaseResponse{Accepted: true, LockExpiresAt: timestamppb.New(s.expiry)}, nil
}
func TestGRPCHeartbeatMapping(t *testing.T) {
	for _, tc := range []struct {
		code     codes.Code
		expected error
	}{{codes.OK, nil}, {codes.Unimplemented, client.ErrHeartbeatUnsupported}, {codes.FailedPrecondition, client.ErrLeaseLost}, {codes.Unavailable, nil}} {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		server := grpc.NewServer()
		defer server.Stop()
		expiry := time.Now().Add(time.Minute)
		workflowv1.RegisterWorkflowEngineServer(server, &heartbeatServer{code: tc.code, expiry: expiry})
		go server.Serve(listener)
		engine, err := client.NewGrpc(listener.Addr().String(), "owner", grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatal(err)
		}
		defer engine.Close()
		got, err := engine.ExtendJobLease(t.Context(), "job", "owner", 60)
		if tc.code == codes.OK {
			if err != nil || !got.Equal(expiry) {
				t.Fatalf("expiry=%s err=%v", got, err)
			}
		} else if tc.expected != nil {
			if !errors.Is(err, tc.expected) {
				t.Fatalf("code=%s error=%v", tc.code, err)
			}
		} else if status.Code(err) != codes.Unavailable {
			t.Fatalf("transport error lost: %v", err)
		}
	}
}
