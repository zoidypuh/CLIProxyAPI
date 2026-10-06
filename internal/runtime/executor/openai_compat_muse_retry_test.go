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
)

const museNotFoundResponse = `{"error":{"message":"Provider returned error","code":404,"metadata":{"provider_name":"Meta","provider_error_code":"model_not_found","raw":"{\"error\":{\"code\":\"model_not_found\"}}"}}}`

type museRetryTransport func(*http.Request) (*http.Response, error)

func (f museRetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestOpenAICompatMuseRecoversFromUpstreamNotFound(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "chat"
		if stream {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			attempts := 0
			var firstBody string
			transport := museRetryTransport(func(req *http.Request) (*http.Response, error) {
				attempts++
				body, errRead := io.ReadAll(req.Body)
				if errRead != nil {
					t.Fatal(errRead)
				}
				if attempts == 1 {
					firstBody = string(body)
				} else if string(body) != firstBody {
					t.Fatalf("retry changed the translated request body")
				}
				if req.Header.Get("Authorization") != "Bearer test-key" {
					t.Fatal("retry lost the credential")
				}
				status, response := http.StatusNotFound, museNotFoundResponse
				if attempts > 1 {
					status = http.StatusOK
					response = `{"id":"chatcmpl_test","object":"chat.completion","model":"meta/muse-spark-1.3-contributor","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`
					if stream {
						response = "data: {\"id\":\"chatcmpl_test\",\"model\":\"meta/muse-spark-1.3-contributor\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"OK\"},\"finish_reason\":null}]}\n\ndata: [DONE]\n\n"
					}
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response)), Request: req}, nil
			})
			ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", transport)
			executor := NewOpenAICompatExecutor("openai-compatible-openrouter", &config.Config{})
			auth := &cliproxyauth.Auth{Attributes: map[string]string{"base_url": "https://openrouter.ai/api/v1", "api_key": "test-key"}}
			req := cliproxyexecutor.Request{Model: "meta/muse-spark-1.3-contributor", Payload: []byte(`{"model":"meta/muse-spark-1.3-contributor","messages":[{"role":"user","content":"Reply OK"}],"reasoning_effort":"high","tools":[]}`)}
			opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai"), Stream: stream}
			if stream {
				result, err := executor.ExecuteStream(ctx, auth, req, opts)
				if err != nil {
					t.Fatalf("ExecuteStream should recover from transient Meta 404: %v", err)
				}
				for chunk := range result.Chunks {
					if chunk.Err != nil {
						t.Fatal(chunk.Err)
					}
				}
			} else if _, err := executor.Execute(ctx, auth, req, opts); err != nil {
				t.Fatalf("Execute should recover from transient Meta 404: %v", err)
			}
			if attempts != 2 {
				t.Fatalf("upstream attempts = %d, want 2", attempts)
			}
		})
	}
}
