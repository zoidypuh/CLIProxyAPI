package executor

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestOpenAICompatVideosForwardsNativeXAIRequests(t *testing.T) {
	type seen struct {
		method string
		path   string
		model  string
		auth   string
	}
	var calls []seen
	transport := museRetryTransport(func(req *http.Request) (*http.Response, error) {
		model := ""
		if req.Body != nil {
			body, errRead := io.ReadAll(req.Body)
			if errRead != nil {
				t.Fatal(errRead)
			}
			model = gjson.GetBytes(body, "model").String()
		}
		calls = append(calls, seen{method: req.Method, path: req.URL.Path, model: model, auth: req.Header.Get("Authorization")})
		response := `{"request_id":"vid-123"}`
		if req.Method == http.MethodGet {
			response = `{"status":"done","video":{"url":"https://vidgen.example/v.mp4","duration":6}}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response)), Request: req}, nil
	})
	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", transport)
	executor := NewOpenAICompatExecutor("openai-compatible-fun", &config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{"base_url": "https://api.example.fun/v1", "api_key": "test-key"}}
	videoFormat := sdktranslator.FromString("openai-video")

	for _, endpoint := range []string{"generations", "edits", "extensions"} {
		req := cliproxyexecutor.Request{Model: "grok-imagine-video-1.5", Payload: []byte(`{"model":"fun/grok-imagine-video-1.5","prompt":"wave"}`)}
		opts := cliproxyexecutor.Options{SourceFormat: videoFormat, Metadata: map[string]any{cliproxyexecutor.RequestPathMetadataKey: "/v1/videos/" + endpoint}}
		resp, err := executor.Execute(ctx, auth, req, opts)
		if err != nil {
			t.Fatalf("%s: Execute: %v", endpoint, err)
		}
		if got := gjson.GetBytes(resp.Payload, "request_id").String(); got != "vid-123" {
			t.Fatalf("%s: request_id = %q, want vid-123", endpoint, got)
		}
	}

	retrieve := cliproxyexecutor.Request{Model: "grok-imagine-video-1.5", Payload: []byte(`{"request_id":"vid-123"}`)}
	retrieveOpts := cliproxyexecutor.Options{SourceFormat: videoFormat, Metadata: map[string]any{cliproxyexecutor.RequestPathMetadataKey: "/v1/videos/vid-123"}}
	resp, err := executor.Execute(ctx, auth, retrieve, retrieveOpts)
	if err != nil {
		t.Fatalf("retrieve Execute: %v", err)
	}
	if got := gjson.GetBytes(resp.Payload, "video.url").String(); got != "https://vidgen.example/v.mp4" {
		t.Fatalf("video.url = %q", got)
	}

	want := []seen{
		{http.MethodPost, "/v1/videos/generations", "grok-imagine-video-1.5", "Bearer test-key"},
		{http.MethodPost, "/v1/videos/edits", "grok-imagine-video-1.5", "Bearer test-key"},
		{http.MethodPost, "/v1/videos/extensions", "grok-imagine-video-1.5", "Bearer test-key"},
		{http.MethodGet, "/v1/videos/vid-123", "", "Bearer test-key"},
	}
	if len(calls) != len(want) {
		t.Fatalf("calls = %+v, want %+v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("call %d = %+v, want %+v", i, calls[i], want[i])
		}
	}
}
