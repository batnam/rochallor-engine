package faultproxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestFaultsOccurBeforeOrAfterRealUpstream(t *testing.T) {
	for _, mode := range []string{"fail-once", "lost-response"} {
		t.Run(mode, func(t *testing.T) {
			var upstreamCalls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { upstreamCalls.Add(1); w.WriteHeader(204) }))
			defer upstream.Close()
			proxy, _ := New(upstream.URL, nil)
			server := httptest.NewServer(proxy)
			defer server.Close()
			post(t, server.URL+"/__faults", `{"mode":"`+mode+`","workerPrefix":"worker-go"}`, 200)
			body := `{"workerId":"worker-go-1","variables":{"result":"same"}}`
			post(t, server.URL+"/v1/jobs/job/complete", body, 503)
			post(t, server.URL+"/v1/jobs/job/complete", body, 204)
			want := int32(1)
			if mode == "lost-response" {
				want = 2
			}
			if upstreamCalls.Load() != want {
				t.Fatalf("upstream calls=%d, want %d", upstreamCalls.Load(), want)
			}
			response, err := http.Get(server.URL + "/__faults")
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			var calls map[string]Calls
			if err := json.NewDecoder(response.Body).Decode(&calls); err != nil {
				t.Fatal(err)
			}
			if calls["job"].Complete != 2 || calls["job"].Changed {
				t.Fatalf("calls=%+v", calls)
			}
		})
	}
}

func TestHoldIsReleasedWithoutDroppingCallHistory(t *testing.T) {
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { upstreamCalls.Add(1); w.WriteHeader(204) }))
	defer upstream.Close()
	proxy, _ := New(upstream.URL, nil)
	server := httptest.NewServer(proxy)
	defer server.Close()
	post(t, server.URL+"/__faults", `{"mode":"hold-before"}`, 200)
	done := make(chan error, 1)
	go func() {
		response, err := http.Post(server.URL+"/v1/jobs/job/complete", "application/json", bytes.NewBufferString(`{"workerId":"worker-go-1"}`))
		if response != nil {
			response.Body.Close()
		}
		done <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		proxy.mu.Lock()
		count := len(proxy.state.calls)
		proxy.mu.Unlock()
		if count > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("request never reached proxy")
		}
		time.Sleep(time.Millisecond)
	}
	if upstreamCalls.Load() != 0 {
		t.Fatal("hold forwarded before release")
	}
	post(t, server.URL+"/__faults/release", `{}`, 204)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("release did not unblock")
	}
	if upstreamCalls.Load() != 1 || proxy.state.calls["job"].Complete != 1 {
		t.Fatal("release lost history")
	}
}

func post(t *testing.T, url, body string, want int) {
	t.Helper()
	response, err := http.Post(url, "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if response.StatusCode != want {
		t.Fatalf("POST %s = %d %s; want %d", url, response.StatusCode, data, want)
	}
}
