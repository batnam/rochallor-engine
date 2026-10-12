package kafkarunner

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/batnam/rochallor-engine/workflow-sdk-go/client"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/handler"
	workflowv1 "github.com/batnam/rochallor-engine/workflow-sdk-go/internal/gen/workflow/v1"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/retry"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
)

type callbackClient struct {
	complete func(context.Context, map[string]any) error
	fail     func(context.Context, string, bool) error
}

func (c *callbackClient) PollJobs(context.Context, client.PollJobsRequest) ([]client.Job, error) {
	return nil, nil
}
func (c *callbackClient) CompleteJob(ctx context.Context, _, _ string, vars map[string]any) error {
	return c.complete(ctx, vars)
}
func (c *callbackClient) FailJob(ctx context.Context, _, _, reason string, retryable bool) error {
	return c.fail(ctx, reason, retryable)
}

func record(t *testing.T, dedup string) *kgo.Record {
	t.Helper()
	value, err := proto.Marshal(&workflowv1.JobDispatchEvent{
		JobId: "job-1", JobType: "test", DedupId: dedup, JobPayload: []byte(`{"input":42}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return &kgo.Record{Topic: "workflow.jobs.test", Partition: 0, Offset: 10, Value: value}
}

func testRunner(t *testing.T, engine client.EngineClient, h handler.Handler) *Runner {
	t.Helper()
	registry := handler.New()
	registry.Register("test", h)
	r, err := New(Config{WorkerID: "worker", SeedBrokers: "localhost:9092", JobTypes: []string{"test"}, DedupWindow: time.Millisecond}, engine, registry)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCompletionRetryKeepsHandlerResult(t *testing.T) {
	handlers, completions, failures := 0, 0, 0
	engine := &callbackClient{
		complete: func(ctx context.Context, vars map[string]any) error {
			completions++
			if !reflect.DeepEqual(vars, map[string]any{"answer": float64(42)}) {
				t.Fatalf("completion changed: %#v", vars)
			}
			if completions == 1 {
				vars["answer"] = 99 // A retry must use the frozen outcome.
				return errors.New("response lost after engine commit")
			}
			return nil
		},
		fail: func(context.Context, string, bool) error { failures++; return nil },
	}
	r := testRunner(t, engine, func(context.Context, handler.JobContext) (handler.Result, error) {
		handlers++
		return handler.Result{VariablesToSet: map[string]any{"answer": 42}}, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r.dispatchRecord(ctx, record(t, "dedup-1"))
	if handlers != 1 || completions != 2 || failures != 0 {
		t.Fatalf("handlers=%d completions=%d failures=%d; want 1,2,0", handlers, completions, failures)
	}
}

func TestFailureRetryKeepsReasonAndRetryability(t *testing.T) {
	for _, nonRetryable := range []bool{false, true} {
		t.Run(map[bool]string{false: "retryable", true: "permanent"}[nonRetryable], func(t *testing.T) {
			handlers, failures := 0, 0
			var failure error = errors.New("business failure")
			if nonRetryable {
				failure = &retry.NonRetryable{Cause: failure}
			}
			engine := &callbackClient{
				complete: func(context.Context, map[string]any) error { t.Fatal("unexpected completion"); return nil },
				fail: func(_ context.Context, reason string, retryable bool) error {
					failures++
					if reason != failure.Error() || retryable == nonRetryable {
						t.Fatalf("callback changed: %q %v", reason, retryable)
					}
					if failures == 1 {
						return errors.New("transport unavailable")
					}
					return nil
				},
			}
			r := testRunner(t, engine, func(context.Context, handler.JobContext) (handler.Result, error) {
				handlers++
				return handler.Result{}, failure
			})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			r.dispatchRecord(ctx, record(t, "dedup-fail"))
			if handlers != 1 || failures != 2 {
				t.Fatalf("handlers=%d failures=%d; want 1,2", handlers, failures)
			}
		})
	}
}

func TestPendingCallbackSurvivesDedupTTLAndCancellationDoesNotConfirm(t *testing.T) {
	var r *Runner
	completions, handlers := 0, 0
	engine := &callbackClient{
		complete: func(context.Context, map[string]any) error {
			completions++
			if r.seenRecently("pending") {
				t.Fatal("unconfirmed outcome entered dedup")
			}
			return errors.New("offline")
		},
		fail: func(context.Context, string, bool) error { t.Fatal("completion became failure"); return nil },
	}
	r = testRunner(t, engine, func(context.Context, handler.JobContext) (handler.Result, error) {
		handlers++
		return handler.Result{}, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()
	if err := r.dispatchRecord(ctx, record(t, "pending")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v", err)
	}
	if handlers != 1 || completions < 2 || r.seenRecently("pending") {
		t.Fatalf("handlers=%d completions=%d", handlers, completions)
	}
	// A replacement owner can process the unconfirmed delivery.
	engine.complete = func(context.Context, map[string]any) error { return nil }
	r.cfg.DedupWindow = time.Hour
	if err := r.dispatchRecord(context.Background(), record(t, "pending")); err != nil {
		t.Fatal(err)
	}
	if err := r.dispatchRecord(context.Background(), record(t, "pending")); err != nil {
		t.Fatal(err)
	}
	if handlers != 2 {
		t.Fatalf("confirmed duplicate reran handler: %d", handlers)
	}
}

func TestMalformedRecordStopsAndBadPayloadRequiresFailureAck(t *testing.T) {
	failures := 0
	engine := &callbackClient{
		complete: func(context.Context, map[string]any) error { t.Fatal("unexpected completion"); return nil },
		fail: func(_ context.Context, _ string, retryable bool) error {
			failures++
			if retryable {
				t.Fatal("invalid payload must be non-retryable")
			}
			if failures == 1 {
				return errors.New("offline")
			}
			return nil
		},
	}
	r := testRunner(t, engine, func(context.Context, handler.JobContext) (handler.Result, error) {
		t.Fatal("invalid record ran handler")
		return handler.Result{}, nil
	})
	for _, value := range [][]byte{{0xff}, {}} {
		if err := r.dispatchRecord(context.Background(), &kgo.Record{Value: value}); err == nil {
			t.Fatal("invalid record was acknowledged")
		}
	}
	value, _ := proto.Marshal(&workflowv1.JobDispatchEvent{JobId: "job", JobType: "test", JobPayload: []byte("not json")})
	if err := r.dispatchRecord(context.Background(), &kgo.Record{Value: value}); err != nil {
		t.Fatal(err)
	}
	if failures != 2 {
		t.Fatalf("failure callbacks=%d, want 2", failures)
	}
}

func TestPermanentCallbackRejectionDoesNotConfirm(t *testing.T) {
	calls := 0
	engine := &callbackClient{
		complete: func(context.Context, map[string]any) error { calls++; return &client.HTTPError{StatusCode: 403} },
		fail:     func(context.Context, string, bool) error { t.Fatal("completion became failure"); return nil },
	}
	r := testRunner(t, engine, func(context.Context, handler.JobContext) (handler.Result, error) { return handler.Result{}, nil })
	if err := r.dispatchRecord(context.Background(), record(t, "rejected")); err == nil {
		t.Fatal("rejection was acknowledged")
	}
	if calls != 1 || r.seenRecently("rejected") {
		t.Fatalf("calls=%d or dedup confirmed rejected result", calls)
	}
}

func TestAssignmentLifetimeFencesStaleCommits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	owners := newOwnership(ctx)
	topic := map[string][]int32{"workflow.jobs.test": {0}}
	rec := record(t, "")
	owners.assigned(ctx, nil, topic)
	old := owners.get(rec)
	// An incremental assignment must preserve ownership of retained partitions.
	owners.assigned(ctx, nil, topic)
	if owners.get(rec) != old {
		t.Fatal("retained assignment cancelled pending work")
	}
	owners.revoked(ctx, nil, topic)
	if old.ctx.Err() == nil {
		t.Fatal("revocation did not cancel pending callback")
	}
	owners.assigned(ctx, nil, topic)
	commits := 0
	commit := func(context.Context) error { commits++; return nil }
	if err := owners.commit(rec, old, commit); !errors.Is(err, context.Canceled) {
		t.Fatalf("stale commit=%v", err)
	}
	if err := owners.commit(rec, owners.get(rec), commit); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := owners.commit(rec, owners.get(rec), commit); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown commit=%v", err)
	}
	if commits != 1 {
		t.Fatalf("commits=%d, want 1", commits)
	}
}
