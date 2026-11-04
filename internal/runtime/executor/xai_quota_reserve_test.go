package executor

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

type xaiReserveTransport func(*http.Request) (*http.Response, error)

func (f xaiReserveTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestXAIQuotaReserveBlocksBeforeInference(t *testing.T) {
	billingCalls, inferenceCalls := 0, 0
	ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", xaiReserveTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() == "https://cli-chat-proxy.grok.com/v1/billing?format=credits" {
			billingCalls++
			if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer test-token" {
				t.Errorf("billing request has incorrect method or credentials")
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"config":{"creditUsagePercent":90}}`))}, nil
		}
		inferenceCalls++
		return nil, errors.New("inference must not run")
	}))
	auth := &cliproxyauth.Auth{Provider: "xai", Metadata: map[string]any{
		"email": "thegismar@gmail.com", "auth_kind": "oauth", "access_token": "test-token",
	}}
	_, err := NewXAIExecutor(&config.Config{}).Execute(ctx, auth, cliproxyexecutor.Request{
		Model: "grok-4", Payload: []byte(`{"model":"grok-4","input":"hello"}`),
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse})
	var gate interface {
		error
		StatusCode() int
		IsCredentialScoped() bool
		RetryAfter() *time.Duration
	}
	if !errors.As(err, &gate) || gate.StatusCode() != 429 || !gate.IsCredentialScoped() || gate.RetryAfter() == nil || *gate.RetryAfter() != time.Minute {
		t.Fatalf("want account-wide reserve cooldown, got %T: %v", err, err)
	}
	if billingCalls != 1 || inferenceCalls != 0 {
		t.Fatalf("billing calls=%d inference calls=%d, want 1 and 0", billingCalls, inferenceCalls)
	}
}

func TestXAIQuotaReserveFailsOverAcrossModels(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "nonstream"
		if stream {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			billingCalls, fallbackCalls, protectedCalls := 0, 0, 0
			ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", xaiReserveTransport(func(r *http.Request) (*http.Response, error) {
				body := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"reserve-test\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"
				if r.URL.Path == "/v1/billing" {
					billingCalls++
					body = `{"config":{"creditUsagePercent":90}}`
				} else if r.Header.Get("Authorization") == "Bearer protected-token" {
					protectedCalls++
				} else {
					fallbackCalls++
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			}))
			manager := cliproxyauth.NewManager(nil, &cliproxyauth.FillFirstSelector{}, nil)
			manager.SetRetryConfig(0, 0, 0)
			manager.RegisterExecutor(NewXAIAutoExecutor(&config.Config{}))
			protectedID := "a-reserve-protected-" + name
			for _, account := range []struct{ id, email, token string }{
				{protectedID, "thegismar@gmail.com", "protected-token"},
				{"z-reserve-fallback-" + name, "other@example.com", "fallback-token"},
			} {
				registry.GetGlobalRegistry().RegisterClient(account.id, "xai", []*registry.ModelInfo{{ID: "grok-4"}, {ID: "grok-4-fast"}})
				t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(account.id) })
				if _, err := manager.Register(ctx, &cliproxyauth.Auth{
					ID: account.id, Provider: "xai", Status: cliproxyauth.StatusActive,
					Metadata: map[string]any{"email": account.email, "access_token": account.token, "auth_kind": "oauth"},
				}); err != nil {
					t.Fatal(err)
				}
			}
			for _, model := range []string{"grok-4", "grok-4-fast"} {
				req := cliproxyexecutor.Request{Model: model, Payload: []byte(`{"input":"hello"}`)}
				opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse, Stream: stream}
				if stream {
					resp, err := manager.ExecuteStream(ctx, []string{"xai"}, req, opts)
					if err != nil {
						t.Fatal(err)
					}
					for chunk := range resp.Chunks {
						if chunk.Err != nil {
							t.Fatal(chunk.Err)
						}
					}
				} else if _, err := manager.Execute(ctx, []string{"xai"}, req, opts); err != nil {
					t.Fatal(err)
				}
			}
			protected, ok := manager.GetByID(protectedID)
			if !ok || protected.Quota.Reason != "credential_quota" || !protected.Quota.NextRecoverAt.After(time.Now()) {
				t.Fatal("protected credential did not enter account-wide cooldown")
			}
			if billingCalls != 1 || protectedCalls != 0 || fallbackCalls != 2 {
				t.Fatalf("billing=%d protected=%d fallback=%d, want 1, 0, 2", billingCalls, protectedCalls, fallbackCalls)
			}
		})
	}
}

func TestXAIQuotaReserveCoversExecutionPaths(t *testing.T) {
	for _, path := range []string{"stream", "websocket", "compact", "compaction-stream", "websocket-compaction", "image", "video", "speech"} {
		t.Run(path, func(t *testing.T) {
			var inferenceCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				inferenceCalls.Add(1)
				w.WriteHeader(http.StatusBadGateway)
			}))
			defer server.Close()
			billingCalls := 0
			ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", xaiReserveTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/v1/billing" {
					inferenceCalls.Add(1)
					return nil, errors.New("unexpected inference")
				}
				billingCalls++
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"config":{"creditUsagePercent":95}}`))}, nil
			}))
			auth := &cliproxyauth.Auth{Provider: "xai", Metadata: map[string]any{
				"email": "thegismar@gmail.com", "auth_kind": "oauth", "access_token": "test-token", "base_url": server.URL,
			}}
			req := cliproxyexecutor.Request{Model: "grok-4", Payload: []byte(`{"model":"grok-4","input":"hello"}`)}
			opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse}
			exec := NewXAIExecutor(&config.Config{})
			var err error
			switch path {
			case "stream":
				_, err = exec.ExecuteStream(ctx, auth, req, opts)
			case "websocket":
				_, err = NewXAIWebsocketsExecutor(&config.Config{}).ExecuteStream(ctx, auth, req, opts)
			case "compaction-stream", "websocket-compaction":
				req.Payload = []byte(`{"model":"grok-4","input":[{"type":"compaction_trigger"}]}`)
				if path == "compaction-stream" {
					_, err = exec.ExecuteStream(ctx, auth, req, opts)
				} else {
					_, err = NewXAIWebsocketsExecutor(&config.Config{}).ExecuteStream(ctx, auth, req, opts)
				}
			default:
				if path == "compact" {
					opts.Alt = "responses/compact"
				} else {
					opts.SourceFormat = sdktranslator.FromString("openai-" + path)
				}
				_, err = exec.Execute(ctx, auth, req, opts)
			}
			var scoped interface{ IsCredentialScoped() bool }
			if !errors.As(err, &scoped) || !scoped.IsCredentialScoped() || billingCalls != 1 || inferenceCalls.Load() != 0 {
				t.Fatalf("gate bypass: err=%v billing=%d inference=%d", err, billingCalls, inferenceCalls.Load())
			}
		})
	}
}
