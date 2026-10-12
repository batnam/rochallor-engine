//go:build integration

package invariants_test

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	workflowv1 "github.com/batnam/rochallor-engine/workflow-engine/api/gen/workflow/v1"
	grpcapi "github.com/batnam/rochallor-engine/workflow-engine/internal/api/grpc"
	restapi "github.com/batnam/rochallor-engine/workflow-engine/internal/api/rest"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch/polling"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func leaseAPIs(t *testing.T) (string, workflowv1.WorkflowEngineClient) {
	t.Helper()
	d := polling.New().Dispatcher()
	httpServer := httptest.NewServer(restapi.NewRouter(gDbConn, gJobStore, gDefRepo, gInstSvc, d, "polling", restapi.NewReadiness(t.Context(), gPool.Ping)))
	t.Cleanup(httpServer.Close)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	grpcapi.NewEngineServer(gDefRepo, gInstSvc, gDbConn, gJobStore, d, "polling").Register(server)
	go server.Serve(listener)
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return httpServer.URL, workflowv1.NewWorkflowEngineClient(conn)
}

func TestLeaseAPIExpiryRESTAndGRPC(t *testing.T) {
	base, client := leaseAPIs(t)
	for _, transport := range []string{"rest", "grpc"} {
		for _, seconds := range []int{0, 5, 60, 3600, -1, 4, 3601} {
			t.Run(fmt.Sprintf("%s/%d", transport, seconds), func(t *testing.T) {
				f := newCallbackFixture(t, "polling", 2)
				invalid := seconds < 0 || seconds > 3600 || (seconds > 0 && seconds < 5)
				var expiry time.Time
				if transport == "rest" {
					body := fmt.Sprintf(`{"workerId":"lease-api","jobTypes":[%q],"maxJobs":1,"leaseSeconds":%d}`, f.jobType, seconds)
					response, err := http.Post(base+"/v1/jobs/poll", "application/json", strings.NewReader(body))
					if err != nil {
						t.Fatal(err)
					}
					defer response.Body.Close()
					if invalid {
						if response.StatusCode != 400 {
							t.Fatalf("invalid duration status=%d", response.StatusCode)
						}
						return
					}
					var result struct {
						Jobs []instance.Job `json:"jobs"`
					}
					if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
						t.Fatal(err)
					}
					if response.StatusCode != 200 || len(result.Jobs) != 1 {
						t.Fatalf("status=%d jobs=%d", response.StatusCode, len(result.Jobs))
					}
					expiry = *result.Jobs[0].LockExpiresAt
				} else {
					response, err := client.PollJobs(t.Context(), &workflowv1.PollJobsRequest{WorkerId: "lease-api", JobTypes: []string{f.jobType}, MaxJobs: 1, LeaseSeconds: int32(seconds)})
					if invalid {
						if status.Code(err) != codes.InvalidArgument {
							t.Fatalf("invalid duration err=%v", err)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if len(response.Jobs) != 1 {
						t.Fatalf("jobs=%d", len(response.Jobs))
					}
					expiry = response.Jobs[0].LockExpiresAt.AsTime()
				}
				var dbTime time.Time
				if err := gPool.QueryRow(t.Context(), "SELECT clock_timestamp()").Scan(&dbTime); err != nil {
					t.Fatal(err)
				}
				if seconds == 0 {
					seconds = 30
				}
				remaining := expiry.Sub(dbTime).Seconds()
				if remaining < float64(seconds)-0.5 || remaining > float64(seconds) {
					t.Fatalf("remaining=%f expected=%d", remaining, seconds)
				}
			})
		}
	}
}

func TestHeartbeatAPIContract(t *testing.T) {
	base, client := leaseAPIs(t)
	for _, transport := range []string{"rest", "grpc"} {
		t.Run(transport, func(t *testing.T) {
			f := newCallbackFixture(t, "polling", 2)
			j := f.delivery(t, "owner")
			call := func(id, worker string, seconds, want int) {
				t.Helper()
				if transport == "rest" {
					body := fmt.Sprintf(`{"workerId":%q,"leaseSeconds":%d}`, worker, seconds)
					response, err := http.Post(base+"/v1/jobs/"+id+"/heartbeat", "application/json", strings.NewReader(body))
					if err != nil {
						t.Fatal(err)
					}
					defer response.Body.Close()
					if response.StatusCode != want {
						t.Fatalf("heartbeat status=%d want=%d", response.StatusCode, want)
					}
					var result struct {
						Accepted      bool      `json:"accepted"`
						LockExpiresAt time.Time `json:"lockExpiresAt"`
						Error         string    `json:"error"`
					}
					if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
						t.Fatal(err)
					}
					if want == 200 && (!result.Accepted || result.LockExpiresAt.Before(*j.LockExpiresAt)) {
						t.Fatalf("invalid acceptance: %+v", result)
					}
					if want == 409 && result.Error != "lease-lost" {
						t.Fatalf("ambiguous rejection: %+v", result)
					}
				} else {
					response, err := client.ExtendJobLease(t.Context(), &workflowv1.ExtendJobLeaseRequest{JobId: id, WorkerId: worker, LeaseSeconds: int32(seconds)})
					code := codes.OK
					if want == 400 {
						code = codes.InvalidArgument
					}
					if want == 409 {
						code = codes.FailedPrecondition
					}
					if status.Code(err) != code {
						t.Fatalf("heartbeat code=%s want=%s", status.Code(err), code)
					}
					if want == 200 && (!response.Accepted || response.LockExpiresAt.AsTime().Before(*j.LockExpiresAt)) {
						t.Fatalf("invalid acceptance: %+v", response)
					}
				}
			}
			call(j.ID, "owner", 60, 200)
			call(j.ID, "owner", 0, 200)
			call(j.ID, "owner", 5, 200)
			for _, seconds := range []int{-1, 4, 3601} {
				call(j.ID, "owner", seconds, 400)
			}
			call(j.ID, "", 30, 400)
			call(j.ID, "other", 30, 409)
			call("missing-job", "owner", 30, 409)
			f.complete(t, j, "owner")
			call(j.ID, "owner", 30, 409)
		})
	}
}
