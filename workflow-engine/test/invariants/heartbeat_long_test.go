//go:build integration

package invariants_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	workflowv1 "github.com/batnam/rochallor-engine/workflow-engine/api/gen/workflow/v1"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/job"
)

func TestHeartbeatLongTaskAndStoppedWorker(t *testing.T) {
	for _, transport := range []string{"rest", "grpc"} {
		t.Run(transport, func(t *testing.T) {
			t.Parallel()
			base, client := leaseAPIs(t)
			httpClient := &http.Client{Timeout: 5 * time.Second}
			heartbeat := func(id string, seconds int) error {
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				if transport == "grpc" {
					response, err := client.ExtendJobLease(ctx, &workflowv1.ExtendJobLeaseRequest{JobId: id, WorkerId: "long-worker", LeaseSeconds: int32(seconds)})
					if err != nil {
						return err
					}
					if !response.Accepted {
						return fmt.Errorf("heartbeat not accepted")
					}
					return nil
				}
				request, err := http.NewRequestWithContext(ctx, "POST", base+"/v1/jobs/"+id+"/heartbeat", strings.NewReader(fmt.Sprintf(`{"workerId":"long-worker","leaseSeconds":%d}`, seconds)))
				if err != nil {
					return err
				}
				request.Header.Set("Content-Type", "application/json")
				response, err := httpClient.Do(request)
				if err != nil {
					return err
				}
				defer response.Body.Close()
				var result struct {
					Accepted bool `json:"accepted"`
				}
				if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
					return err
				}
				if response.StatusCode != 200 || !result.Accepted {
					return fmt.Errorf("heartbeat status=%d", response.StatusCode)
				}
				return nil
			}
			ctx, stop := context.WithCancel(t.Context())
			defer stop()
			f := newCallbackFixture(t, "polling", 2)
			j := f.delivery(t, "long-worker")
			job.StartLeaseSweeper(ctx, gDbConn, gJobStore, f.dispatcher, 100*time.Millisecond)
			started := time.Now()
			// A real 90-second handler window, not a shorter simulated lease.
			for tick := range 9 {
				time.Sleep(time.Until(started.Add(time.Duration(tick+1) * 10 * time.Second)))
				if err := heartbeat(j.ID, 30); err != nil {
					t.Fatal(err)
				}
				if (tick+1)%3 == 0 {
					t.Logf("%s handler still owns lease at %ds", transport, (tick+1)*10)
				}
			}
			f.complete(t, j, "long-worker")
			var total, completed int
			if err := gPool.QueryRow(f.ctx, `SELECT count(*),count(*) FILTER (WHERE status='COMPLETED') FROM job WHERE instance_id=$1`, f.inst.ID).Scan(&total, &completed); err != nil {
				t.Fatal(err)
			}
			if total != 1 || completed != 1 {
				t.Fatalf("long task: attempts=%d completed=%d", total, completed)
			}
			got, err := f.svc.Get(f.ctx, f.inst.ID)
			if err != nil || got.Status != instance.InstanceStatusCompleted {
				t.Fatalf("long task outcome=%+v err=%v", got, err)
			}

			// A second worker stops renewing. Recovery must create exactly one
			// fresh attempt, preserve business retries and fence the old job ID.
			f = newCallbackFixture(t, "polling", 2)
			jobs, err := job.PollWithLease(f.ctx, gJobStore, "long-worker", []string{f.jobType}, 1, 5)
			if err != nil || len(jobs) != 1 {
				t.Fatalf("short lease poll: %v", err)
			}
			old := jobs[0]
			if err := heartbeat(old.ID, 5); err != nil {
				t.Fatal(err)
			}
			var fresh instance.Job
			deadline := time.Now().Add(9 * time.Second)
			for time.Now().Before(deadline) {
				jobs, err := job.Poll(f.ctx, gJobStore, "replacement-worker", []string{f.jobType}, 1)
				if err != nil {
					t.Fatal(err)
				}
				if len(jobs) > 0 {
					fresh = jobs[0]
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			if fresh.ID == "" || fresh.ID == old.ID || fresh.RetriesRemaining != old.RetriesRemaining {
				t.Fatalf("stopped worker recovery: %+v", fresh)
			}
			before := f.snapshot(t)
			if err := heartbeat(old.ID, 30); err == nil {
				t.Fatal("stopped attempt was revived")
			}
			f.complete(t, old, "long-worker")
			f.assertUnchanged(t, before)
			f.complete(t, fresh, "replacement-worker")
			if err := gPool.QueryRow(f.ctx, `SELECT count(*) FROM job WHERE instance_id=$1`, f.inst.ID).Scan(&total); err != nil {
				t.Fatal(err)
			}
			if total != 2 {
				t.Fatalf("expected one replacement: %d", total)
			}
		})
	}
}
