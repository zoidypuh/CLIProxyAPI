package helps

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

const museRetryError = `{"error":{"message":"Provider returned error","code":404,"metadata":{"provider_name":"Meta","provider_error_code":"model_not_found"}}}`

func TestOpenRouterMuseNotFoundMatchesStructuredUpstreamError(t *testing.T) {
	for _, test := range []struct {
		name, body string
		want       bool
	}{
		{"provider code", museRetryError, true},
		{"raw provider code", `{"error":{"message":"Provider returned error","metadata":{"provider_name":"Meta","raw":"{\"error\":{\"code\":\"model_not_found\"}}"}}}`, true},
		{"other explicit code", strings.Replace(museRetryError, "model_not_found", "permission_denied", 1), false},
		{"missing metadata", `{"error":{"message":"Provider returned error"}}`, false},
		{"missing raw code", `{"error":{"message":"Provider returned error","metadata":{"provider_name":"Meta","raw":"{}"}}}`, false},
		{"invalid raw JSON", `{"error":{"message":"Provider returned error","metadata":{"provider_name":"Meta","raw":"invalid"}}}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := isOpenRouterMuseNotFound([]byte(test.body)); got != test.want {
				t.Fatalf("matched = %v, want %v", got, test.want)
			}
		})
	}
}

func TestOpenRouterMuseRetryIsBounded(t *testing.T) {
	attempts := 0
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{StatusCode: http.StatusNotFound, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(museRetryError)), Request: req}, nil
	})}
	req, err := http.NewRequest(http.MethodPost, "https://openrouter.ai/api/v1/chat/completions", bytes.NewBufferString(`{"model":"meta/muse-spark-1.3-contributor"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := DoOpenAICompatRequest(client, req, openRouterMuseModel, nil, UpstreamRequestLog{})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 3 || resp.StatusCode != http.StatusNotFound || string(body) != museRetryError {
		t.Fatalf("persistent failure: attempts=%d status=%d body=%s; want 3 attempts and original 404", attempts, resp.StatusCode, body)
	}
}

func TestOpenRouterMuseRetryLeavesOtherFailuresUntouched(t *testing.T) {
	for _, test := range []struct {
		name, url, model, body string
		status                 int
	}{
		{"different model", "https://openrouter.ai/api/v1/chat/completions", "another-model", museRetryError, 404},
		{"different endpoint", "https://example.com/api/v1/chat/completions", openRouterMuseModel, museRetryError, 404},
		{"different path", "https://openrouter.ai/api/v1/responses/compact", openRouterMuseModel, museRetryError, 404},
		{"different provider", "https://openrouter.ai/api/v1/chat/completions", openRouterMuseModel, strings.Replace(museRetryError, "Meta", "Other", 1), 404},
		{"ordinary not found", "https://openrouter.ai/api/v1/chat/completions", openRouterMuseModel, `{"error":{"code":"model_not_found"}}`, 404},
		{"auth failure", "https://openrouter.ai/api/v1/chat/completions", openRouterMuseModel, museRetryError, 401},
		{"malformed response", "https://openrouter.ai/api/v1/chat/completions", openRouterMuseModel, "not JSON", 404},
		{"success containing an error", "https://openrouter.ai/api/v1/chat/completions", openRouterMuseModel, museRetryError, 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			attempts := 0
			client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				attempts++
				return &http.Response{StatusCode: test.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(test.body)), Request: req}, nil
			})}
			req, err := http.NewRequest(http.MethodPost, test.url, bytes.NewBufferString(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			resp, err := DoOpenAICompatRequest(client, req, test.model, nil, UpstreamRequestLog{})
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if attempts != 1 || resp.StatusCode != test.status || string(body) != test.body {
				t.Fatalf("unrelated response changed: attempts=%d status=%d body=%s", attempts, resp.StatusCode, body)
			}
		})
	}
}

func TestOpenRouterMuseRetryStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	attempts := 0
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		attempts++
		cancel()
		return &http.Response{StatusCode: http.StatusNotFound, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(museRetryError)), Request: req}, nil
	})}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://openrouter.ai/api/v1/chat/completions", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = DoOpenAICompatRequest(client, req, openRouterMuseModel, nil, UpstreamRequestLog{})
	if !errors.Is(err, context.Canceled) || attempts != 1 {
		t.Fatalf("cancellation: attempts=%d err=%v", attempts, err)
	}
}
