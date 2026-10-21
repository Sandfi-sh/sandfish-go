package sandfish

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile("testdata/contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var all map[string]map[string]any
	if err := json.Unmarshal(data, &all); err != nil {
		t.Fatal(err)
	}
	return all[name]
}

func reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func TestSubmissionAndNullSerialization(t *testing.T) {
	request := NewSubmitUrlRequest("https://example.com/")
	data, _ := json.Marshal(request)
	if bytes.Contains(data, []byte("visibility")) {
		t.Fatal("omitted visibility was serialized")
	}
	request.SetVisibilityNil()
	data, _ = json.Marshal(request)
	if !bytes.Contains(data, []byte(`"visibility":null`)) {
		t.Fatal("explicit null was lost")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/analyses" || r.Header.Get("Authorization") != "Bearer ak_example" {
			t.Error("wrong URL or authentication")
		}
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte(`"visibility":null`)) {
			t.Error("wire null was lost")
		}
		reply(w, 202, fixture(t, "accepted"))
	}))
	defer server.Close()
	client, err := NewClient(Options{APIKey: "ak_example", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	accepted, _, err := client.AnalysesAPI.SubmitUrl(context.Background()).SubmitUrlRequest(*request).Execute()
	if err != nil {
		t.Fatal(err)
	}
	if accepted.OperationId != fixture(t, "accepted")["operation_id"] {
		t.Fatal("wrong operation ID")
	}
}

func TestPollingTerminalStates(t *testing.T) {
	for _, status := range []string{"succeeded", "failed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				operation := fixture(t, "operation")
				operation["status"] = status
				if calls == 1 {
					operation["status"] = "running"
				}
				reply(w, 200, operation)
			}))
			defer server.Close()
			client, _ := NewClient(Options{BaseURL: server.URL})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			operation, err := client.WaitForOperation(ctx, fixture(t, "operation")["id"].(string), time.Millisecond)
			if calls != 2 {
				t.Fatalf("got %d requests", calls)
			}
			if status == "succeeded" && err != nil {
				t.Fatal(err)
			}
			var failure *OperationError
			if status != "succeeded" && !errors.As(err, &failure) {
				t.Fatalf("expected operation error: %v", err)
			}
			if string(operation.Status) != status {
				t.Fatal("wrong terminal state")
			}
		})
	}
}

func TestReadRetriesAndNoSubmissionRetry(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if len(methods) == 2 {
			reply(w, 200, fixture(t, "operation"))
			return
		}
		w.Header().Set("Retry-After", "0")
		w.Header().Set("X-Request-ID", "request-fixture")
		reply(w, 429, map[string]any{"error": map[string]any{"code": "rate_limited", "message": "Wait"}})
	}))
	defer server.Close()
	client, _ := NewClient(Options{BaseURL: server.URL, MaxRetries: 2})
	_, _, err := client.OperationsAPI.GetOperation(context.Background(), fixture(t, "operation")["id"].(string)).Execute()
	if err != nil {
		t.Fatal(err)
	}
	_, response, err := client.AnalysesAPI.SubmitUrl(context.Background()).SubmitUrlRequest(*NewSubmitUrlRequest("https://example.com/")).Execute()
	if err == nil || response.StatusCode != 429 {
		t.Fatal("expected a rate-limit error")
	}
	if len(methods) != 3 || methods[0] != "GET" || methods[1] != "GET" || methods[2] != "POST" {
		t.Fatalf("unexpected methods: %v", methods)
	}
	if response.Header.Get("X-Request-ID") != "request-fixture" {
		t.Fatal("request ID was lost")
	}
}

func TestSearchDiscriminator(t *testing.T) {
	data, _ := json.Marshal(fixture(t, "search"))
	var search SearchResponse
	if err := json.Unmarshal(data, &search); err != nil {
		t.Fatal(err)
	}
	if len(search.Items) != 1 || search.Items[0].GroupSearchResult == nil {
		t.Fatal("group discriminator was lost")
	}
}

func TestPaginationAndStreaming(t *testing.T) {
	var offsets []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/analyses/example/screenshot" {
			w.Write([]byte{0x89, 0x50, 0x4e, 0x47, 0, 0xff})
			return
		}
		offsets = append(offsets, r.URL.Query().Get("offset"))
		reply(w, 200, map[string]any{"items": []any{fixture(t, "analysis")}, "limit": 1, "offset": len(offsets) - 1, "total": 2})
	}))
	defer server.Close()
	client, _ := NewClient(Options{BaseURL: server.URL})
	count := 0
	err := client.ForEachAnalysis(context.Background(), 10, func(item AnalysisSummary) error { count++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || offsets[0] != "0" || offsets[1] != "1" {
		t.Fatalf("pagination failed: %v", offsets)
	}
	var output bytes.Buffer
	n, err := client.Download(context.Background(), "/api/analyses/example/screenshot", &output)
	if err != nil || n != 6 || !bytes.Equal(output.Bytes(), []byte{0x89, 0x50, 0x4e, 0x47, 0, 0xff}) {
		t.Fatalf("download failed: %v", err)
	}
}

func TestPollingCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client, _ := NewClient(Options{BaseURL: "http://127.0.0.1:1"})
	_, err := client.WaitForOperation(ctx, "example", time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation: %v", err)
	}
}
