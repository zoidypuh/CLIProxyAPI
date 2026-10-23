package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

func TestMetaExecutorImagesGenerationsUsesImagesPath(t *testing.T) {
	var gotPath, gotAuth, gotModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		gotModel = strings.TrimSpace(string(body))
		if !strings.Contains(gotModel, `"model":"muse-image-1.0"`) || !strings.Contains(gotModel, `"prompt":"A red fox"`) || !strings.Contains(gotModel, `"n":1`) {
			t.Errorf("unexpected body: %s", gotModel)
		}
		if strings.Contains(gotModel, `"stream"`) {
			t.Errorf("image body should not include stream: %s", gotModel)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"created":1,"data":[{"b64_json":"abc"}]}`))
	}))
	defer server.Close()

	exec := NewMetaExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		ID:       "meta-image",
		Provider: "meta",
		Attributes: map[string]string{
			"api_key":  "meta-test-key",
			"base_url": server.URL + "/v1",
		},
	}
	resp, err := exec.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "meta/muse-image-1.0",
		Payload: []byte(`{"model":"meta/muse-image-1.0","prompt":"A red fox","n":1,"stream":true}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai-image"),
		Metadata: map[string]any{
			cliproxyexecutor.RequestPathMetadataKey: "/v1/images/generations",
		},
	})
	if err != nil {
		t.Fatalf("execute images: %v", err)
	}
	if gotPath != "/v1/images/generations" {
		t.Fatalf("path = %q, want /v1/images/generations", gotPath)
	}
	if gotAuth != "Bearer meta-test-key" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if !strings.Contains(string(resp.Payload), `"b64_json":"abc"`) {
		t.Fatalf("payload = %s", resp.Payload)
	}
}

func TestMetaExecutorChatStillUsesResponses(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"))
	}))
	defer server.Close()

	exec := NewMetaExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		ID:       "meta-chat",
		Provider: "meta",
		Attributes: map[string]string{
			"api_key":  "meta-test-key",
			"base_url": server.URL + "/v1",
		},
	}
	_, err := exec.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "muse-spark-1.3",
		Payload: []byte(`{"model":"muse-spark-1.3","input":"hi"}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai"),
	})
	if err != nil {
		t.Fatalf("execute chat: %v", err)
	}
	if gotPath != "/v1/responses" {
		t.Fatalf("path = %q, want /v1/responses", gotPath)
	}
}
