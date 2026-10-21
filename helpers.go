package sandfish

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Options configures API access. MaxRetries applies only to GET and HEAD.
type Options struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
	MaxRetries int
}

// NewClient creates a configured client. A zero MaxRetries disables retries.
func NewClient(options Options) (*APIClient, error) {
	base := strings.TrimRight(options.BaseURL, "/")
	if base == "" {
		base = "https://sandfi.sh"
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" || strings.HasSuffix(u.Path, "/api") {
		return nil, fmt.Errorf("BaseURL must be an HTTP server URL without /api, query, or fragment")
	}
	if options.MaxRetries < 0 {
		return nil, fmt.Errorf("MaxRetries must be nonnegative")
	}
	config := NewConfiguration()
	config.Servers = ServerConfigurations{{URL: base}}
	config.UserAgent = "sandfish-go " + config.UserAgent
	if options.APIKey != "" {
		config.AddDefaultHeader("Authorization", "Bearer "+options.APIKey)
	}
	client := http.Client{Timeout: 30 * time.Second}
	if options.HTTPClient != nil {
		client = *options.HTTPClient
	}
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = &retryTransport{base: transport, retries: options.MaxRetries}
	config.HTTPClient = &client
	return NewAPIClient(config), nil
}

type retryTransport struct {
	base    http.RoundTripper
	retries int
}

func (t *retryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		response, err := t.base.RoundTrip(request)
		if err != nil {
			return nil, err
		}
		status := response.StatusCode
		if (request.Method != "GET" && request.Method != "HEAD") || attempt >= t.retries || (status != 429 && status != 502 && status != 503 && status != 504) {
			return response, nil
		}
		delay := time.Second * time.Duration(1<<min(attempt, 5))
		if value := response.Header.Get("Retry-After"); value != "" {
			if seconds, err := strconv.ParseUint(value, 10, 32); err == nil {
				delay = time.Duration(seconds) * time.Second
			} else if date, err := http.ParseTime(value); err == nil {
				delay = max(0, time.Until(date))
			}
		}
		response.Body.Close()
		if err := wait(request.Context(), delay); err != nil {
			return nil, err
		}
	}
}

func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// OperationError retains a failed or cancelled operation.
type OperationError struct{ Operation *OperationOut }

func (e *OperationError) Error() string {
	return fmt.Sprintf("operation %s %s", e.Operation.Id, e.Operation.Status)
}

// WaitForOperation polls until completion. Use a context deadline to bound the wait.
func (c *APIClient) WaitForOperation(ctx context.Context, id string, interval time.Duration) (*OperationOut, error) {
	if interval <= 0 {
		return nil, fmt.Errorf("poll interval must be positive")
	}
	for {
		operation, _, err := c.OperationsAPI.GetOperation(ctx, id).Execute()
		if err != nil {
			return nil, err
		}
		switch string(operation.Status) {
		case "succeeded":
			return operation, nil
		case "failed", "cancelled":
			return operation, &OperationError{Operation: operation}
		}
		if err := wait(ctx, interval); err != nil {
			return nil, err
		}
	}
}

// ForEachAnalysis visits visible analyses with offset pagination.
func (c *APIClient) ForEachAnalysis(ctx context.Context, limit int32, visit func(AnalysisSummary) error) error {
	if limit <= 0 {
		return fmt.Errorf("page limit must be positive")
	}
	var offset int32
	for {
		page, _, err := c.AnalysesAPI.ListAnalyses(ctx).Limit(limit).Offset(offset).Execute()
		if err != nil {
			return err
		}
		for _, item := range page.Items {
			if err := visit(item); err != nil {
				return err
			}
		}
		next := int64(page.Offset) + int64(page.Limit)
		if len(page.Items) == 0 || next >= int64(page.Total) {
			return nil
		}
		if page.Limit <= 0 || next <= int64(offset) || next > 2147483647 {
			return fmt.Errorf("API returned a non-advancing page")
		}
		offset = int32(next)
	}
}

// HTTPError describes a failed streaming download.
type HTTPError struct {
	Status    int
	RequestID string
	Code      string
	Message   string
	Details   any
}

func (e *HTTPError) Error() string { return fmt.Sprintf("Sandfish HTTP %d: %s", e.Status, e.Message) }

// Download streams an API response to destination without buffering the full file.
func (c *APIClient) Download(ctx context.Context, apiPath string, destination io.Writer) (int64, error) {
	if !strings.HasPrefix(apiPath, "/api/") {
		return 0, fmt.Errorf("apiPath must start with /api/")
	}
	base, err := c.cfg.ServerURL(0, nil)
	if err != nil {
		return 0, err
	}
	request, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(base, "/")+apiPath, nil)
	if err != nil {
		return 0, err
	}
	for key, value := range c.cfg.DefaultHeader {
		request.Header.Set(key, value)
	}
	request.Header.Set("User-Agent", c.cfg.UserAgent)
	response, err := c.cfg.HTTPClient.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		detail := &HTTPError{Status: response.StatusCode, RequestID: response.Header.Get("X-Request-ID"), Code: "http_error", Message: response.Status}
		var envelope struct {
			Error struct {
				Code      string `json:"code"`
				Message   string `json:"message"`
				RequestID string `json:"request_id"`
				Details   any    `json:"details"`
			} `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(response.Body, 1024*1024)).Decode(&envelope) == nil {
			if envelope.Error.Code != "" {
				detail.Code = envelope.Error.Code
			}
			if envelope.Error.Message != "" {
				detail.Message = envelope.Error.Message
			}
			if envelope.Error.RequestID != "" {
				detail.RequestID = envelope.Error.RequestID
			}
			detail.Details = envelope.Error.Details
		}
		return 0, detail
	}
	return io.Copy(destination, response.Body)
}
