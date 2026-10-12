// Package faultproxy injects callback transport failures in the disposable E2E
// stack. Worker requests still use the real REST/gRPC engine and Kafka broker.
package faultproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	workflowv1 "github.com/batnam/rochallor-engine/workflow-engine/api/gen/workflow/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Rule struct {
	Mode         string `json:"mode"` // fail-once, lost-response, hold-before, hold-after, reject
	Callback     string `json:"callback"`
	WorkerPrefix string `json:"workerPrefix"`
	Match        string `json:"match"`
}

type Calls struct {
	Complete     int    `json:"complete"`
	Fail         int    `json:"fail"`
	Acknowledged int    `json:"acknowledged"`
	Payload      string `json:"payload"`
	Changed      bool   `json:"changed"`
}

type Claim struct {
	Worker     string `json:"worker"`
	MaxJobs    int    `json:"maxJobs"`
	Deadline   string `json:"deadline"`
	InstanceID string `json:"instanceId"`
}

type Heartbeat struct {
	Count    int    `json:"count"`
	Requests int    `json:"requests"`
	Deadline string `json:"deadline"`
}

type pollRequest struct {
	Worker  string `json:"workerId"`
	MaxJobs int    `json:"maxJobs"`
}
type pollKey struct{}

type state struct {
	rule       Rule
	calls      map[string]*Calls
	claims     map[string]Claim
	heartbeats map[string]Heartbeat
	release    chan struct{}
}

type Proxy struct {
	workflowv1.UnimplementedWorkflowEngineServer
	mu      sync.Mutex
	state   *state
	rest    *url.URL
	reverse *httputil.ReverseProxy
	grpc    workflowv1.WorkflowEngineClient
}

func New(rest string, grpc workflowv1.WorkflowEngineClient) (*Proxy, error) {
	u, err := url.Parse(rest)
	if err != nil {
		return nil, err
	}
	p := &Proxy{rest: u, reverse: httputil.NewSingleHostReverseProxy(u), grpc: grpc,
		state: &state{calls: map[string]*Calls{}, claims: map[string]Claim{}, heartbeats: map[string]Heartbeat{}, release: make(chan struct{})}}
	p.reverse.ModifyResponse = func(response *http.Response) error {
		request, ok := response.Request.Context().Value(pollKey{}).(pollRequest)
		parts := strings.Split(strings.Trim(response.Request.URL.Path, "/"), "/")
		isHeartbeat := len(parts) == 4 && parts[0] == "v1" && parts[1] == "jobs" && parts[3] == "heartbeat"
		if (!ok && !isHeartbeat) || response.StatusCode != 200 {
			return nil
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			return err
		}
		response.Body = io.NopCloser(bytes.NewReader(body))
		response.ContentLength = int64(len(body))
		response.Header.Set("Content-Length", strconv.Itoa(len(body)))
		if isHeartbeat {
			var result struct {
				Accepted      bool
				LockExpiresAt string
			}
			if err := json.Unmarshal(body, &result); err != nil {
				return err
			}
			if result.Accepted {
				p.heartbeat(parts[2], result.LockExpiresAt)
			}
			return nil
		}
		var result struct {
			Jobs []struct{ ID, JobType, InstanceID, LockExpiresAt string }
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return err
		}
		for _, job := range result.Jobs {
			p.claim(job.ID, job.JobType, Claim{request.Worker, request.MaxJobs, job.LockExpiresAt, job.InstanceID})
		}
		return nil
	}
	return p, nil
}

func (p *Proxy) heartbeat(id, deadline string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	h := p.state.heartbeats[id]
	h.Count++
	h.Deadline = deadline
	p.state.heartbeats[id] = h
}

func (p *Proxy) heartbeatRequest(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	h := p.state.heartbeats[id]
	h.Requests++
	p.state.heartbeats[id] = h
}

func (p *Proxy) claim(id, jobType string, claim Claim) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if strings.Contains(jobType, "-sdk-") && strings.HasPrefix(claim.Worker, p.state.rule.WorkerPrefix) {
		p.state.claims[id] = claim
	}
}

func (p *Proxy) intercept(ctx context.Context, jobID, worker, kind, payload string, send func() error) error {
	p.mu.Lock()
	s := p.state
	if !strings.HasPrefix(worker, s.rule.WorkerPrefix) || !strings.Contains(payload, s.rule.Match) {
		p.mu.Unlock()
		return send()
	}
	call := s.calls[jobID]
	if call == nil {
		call = &Calls{Payload: payload}
		s.calls[jobID] = call
	}
	if call.Payload != payload {
		call.Changed = true
	}
	if kind == "complete" {
		call.Complete++
	} else {
		call.Fail++
	}
	first := call.Complete+call.Fail == 1
	mode := s.rule.Mode
	release := s.release
	if s.rule.Callback != "" && s.rule.Callback != kind {
		mode = ""
	}
	p.mu.Unlock()

	wait := func() error {
		select {
		case <-ctx.Done():
			return status.FromContextError(ctx.Err()).Err()
		case <-release:
			return nil
		}
	}
	if mode == "reject" {
		return status.Error(codes.PermissionDenied, "injected permanent rejection")
	}
	if mode == "fail-once" && first {
		return status.Error(codes.Unavailable, "injected before engine callback")
	}
	if mode == "hold-before" {
		if err := wait(); err != nil {
			return err
		}
	}
	if err := send(); err != nil {
		return err
	}
	p.mu.Lock()
	call.Acknowledged++
	p.mu.Unlock()
	if mode == "lost-response" && first {
		return status.Error(codes.Unavailable, "injected response loss after engine commit")
	}
	if mode == "hold-after" {
		return wait()
	}
	return nil
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/__heartbeats" {
		p.mu.Lock()
		defer p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(p.state.heartbeats)
		return
	}
	if r.URL.Path == "/__claims" {
		p.mu.Lock()
		defer p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(p.state.claims)
		return
	}
	if r.URL.Path == "/v1/jobs/poll" {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		var request pollRequest
		if err := json.Unmarshal(body, &request); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		p.reverse.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), pollKey{}, request)))
		return
	}
	if r.URL.Path == "/__faults/release" && r.Method == http.MethodPost {
		p.mu.Lock()
		close(p.state.release)
		p.state.release = make(chan struct{})
		p.state.rule.Mode = ""
		p.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.URL.Path == "/__faults" {
		p.mu.Lock()
		defer p.mu.Unlock()
		if r.Method == http.MethodPost {
			var rule Rule
			if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			switch rule.Mode {
			case "", "fail-once", "lost-response", "hold-before", "hold-after", "reject":
			default:
				http.Error(w, "unknown fault mode", 400)
				return
			}
			close(p.state.release)
			p.state = &state{rule: rule, calls: map[string]*Calls{}, claims: map[string]Claim{}, heartbeats: map[string]Heartbeat{}, release: make(chan struct{})}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(p.state.calls)
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) == 4 && parts[0] == "v1" && parts[1] == "jobs" && parts[3] == "heartbeat" {
		p.heartbeatRequest(parts[2])
	}
	if len(parts) != 4 || parts[0] != "v1" || parts[1] != "jobs" || (parts[3] != "complete" && parts[3] != "fail") {
		p.reverse.ServeHTTP(w, r)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	worker, _ := fields["workerId"].(string)
	delete(fields, "workerId")
	canonical, _ := json.Marshal(fields)
	var response *http.Response
	var data []byte
	err = p.intercept(r.Context(), parts[2], worker, parts[3], string(canonical), func() error {
		request := r.Clone(r.Context())
		request.URL.Scheme, request.URL.Host = p.rest.Scheme, p.rest.Host
		request.RequestURI = ""
		request.Body = io.NopCloser(bytes.NewReader(body))
		var err error
		response, err = http.DefaultTransport.RoundTrip(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		data, err = io.ReadAll(response.Body)
		if err != nil {
			return err
		}
		if response.StatusCode >= 400 {
			return fmt.Errorf("upstream returned %d", response.StatusCode)
		}
		return nil
	})
	if err != nil && (response == nil || response.StatusCode < 400) {
		code := http.StatusServiceUnavailable
		if status.Code(err) == codes.PermissionDenied {
			code = http.StatusForbidden
		}
		http.Error(w, err.Error(), code)
		return
	}
	for key, values := range response.Header {
		w.Header()[key] = values
	}
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(data)
}

func (p *Proxy) PollJobs(ctx context.Context, request *workflowv1.PollJobsRequest) (*workflowv1.PollJobsResponse, error) {
	response, err := p.grpc.PollJobs(ctx, request)
	if err == nil {
		for _, job := range response.Jobs {
			p.claim(job.Id, job.JobType, Claim{request.WorkerId, int(request.MaxJobs), job.LockExpiresAt.AsTime().Format(time.RFC3339Nano), job.InstanceId})
		}
	}
	return response, err
}

func (p *Proxy) CompleteJob(ctx context.Context, request *workflowv1.CompleteJobRequest) (*workflowv1.CompleteJobResponse, error) {
	payload, _ := json.Marshal(request.VariablesToSet.AsMap())
	var response *workflowv1.CompleteJobResponse
	err := p.intercept(ctx, request.JobId, request.WorkerId, "complete", string(payload), func() error {
		var err error
		response, err = p.grpc.CompleteJob(ctx, request)
		return err
	})
	return response, err
}

func (p *Proxy) FailJob(ctx context.Context, request *workflowv1.FailJobRequest) (*workflowv1.FailJobResponse, error) {
	payload, _ := json.Marshal(map[string]any{"reason": request.ErrorMessage, "retryable": request.Retryable})
	var response *workflowv1.FailJobResponse
	err := p.intercept(ctx, request.JobId, request.WorkerId, "fail", string(payload), func() error {
		var err error
		response, err = p.grpc.FailJob(ctx, request)
		return err
	})
	return response, err
}

func (p *Proxy) ExtendJobLease(ctx context.Context, request *workflowv1.ExtendJobLeaseRequest) (*workflowv1.ExtendJobLeaseResponse, error) {
	p.heartbeatRequest(request.JobId)
	response, err := p.grpc.ExtendJobLease(ctx, request)
	if err == nil && response.Accepted && response.LockExpiresAt != nil {
		p.heartbeat(request.JobId, response.LockExpiresAt.AsTime().Format(time.RFC3339Nano))
	}
	return response, err
}
