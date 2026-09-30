package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestOpenRouterBatchExecutorPollsUntilCompletion(t *testing.T) {
	var mu sync.Mutex
	polls := 0
	var submitBody []byte
	var submitPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodPost {
			submitPath = r.URL.Path
			submitBody, _ = io.ReadAll(r.Body)
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer test-") {
				t.Errorf("authorization = %q", r.Header.Get("Authorization"))
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"batch_123","object":"batch","status":"validating","results":null}`))
			return
		}
		polls++
		w.Header().Set("Content-Type", "application/json")
		if polls < 2 {
			_, _ = w.Write([]byte(`{"id":"batch_123","object":"batch","status":"in_progress","results":null}`))
			return
		}
		customID := gjson.GetBytes(submitBody, "requests.0.custom_id").String()
		_, _ = w.Write([]byte(`{
			"id":"batch_123",
			"object":"batch",
			"status":"completed",
			"results":[{
				"custom_id":"` + customID + `",
				"response":{
					"status_code":200,
					"body":{
						"id":"gen-batch-1",
						"object":"chat.completion",
						"model":"anthropic/claude-opus-5.5-20260921",
						"choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],
						"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}
					}
				},
				"error":null
			}]
		}`))
	}))
	defer server.Close()

	exec := NewOpenRouterBatchExecutor("openai-compatible-openrouter-batch", &config.Config{})
	auth := &cliproxyauth.Auth{
		ID:       "openrouter-batch",
		Provider: "openai-compatible-openrouter-batch",
		Attributes: map[string]string{
			"base_url": server.URL + "/api/v1",
			"api_key":  "test-key",
		},
	}
	payload := []byte(`{"model":"anthropic/claude-opus-5.5:batch","messages":[{"role":"user","content":"ping"}]}`)
	resp, err := exec.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "anthropic/claude-opus-5.5:batch",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai"),
		Stream:       false,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if submitPath != "/api/v1/batches" {
		t.Fatalf("submit path = %q", submitPath)
	}
	if gjson.GetBytes(submitBody, "endpoint").String() != "/v1/chat/completions" {
		t.Fatalf("endpoint = %s", gjson.GetBytes(submitBody, "endpoint").Raw)
	}
	if gjson.GetBytes(submitBody, "model").String() != "anthropic/claude-opus-5.5" {
		t.Fatalf("batch model = %s", gjson.GetBytes(submitBody, "model").String())
	}
	if strings.Contains(gjson.GetBytes(submitBody, "requests.0.body.model").String(), ":batch") {
		t.Fatalf("request body still has :batch suffix: %s", gjson.GetBytes(submitBody, "requests.0.body.model").String())
	}
	if gjson.GetBytes(submitBody, "requests.0.body.messages.0.content").String() != "ping" {
		t.Fatalf("request content = %s", string(submitBody))
	}
	if gjson.GetBytes(resp.Payload, "choices.0.message.content").String() != "pong" {
		t.Fatalf("completion = %s", string(resp.Payload))
	}
	if polls < 2 {
		t.Fatalf("polls = %d, want at least 2", polls)
	}
}

func TestOpenRouterBatchExecutorRetriesNotFoundAfterSubmit(t *testing.T) {
	var mu sync.Mutex
	polls := 0
	var submitBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			submitBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"batch_404","object":"batch","status":"validating","results":null}`))
			return
		}
		polls++
		if polls == 1 {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"Batch job batch_404 not found.","code":404}}`))
			return
		}
		customID := gjson.GetBytes(submitBody, "requests.0.custom_id").String()
		_, _ = w.Write([]byte(`{
			"id":"batch_404",
			"status":"completed",
			"results":[{
				"custom_id":"` + customID + `",
				"response":{"status_code":200,"body":{"id":"gen-1","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"huptier"}}]}},
				"error":null
			}]
		}`))
	}))
	defer server.Close()

	exec := NewOpenRouterBatchExecutor("openai-compatible-openrouter-batch", &config.Config{})
	auth := &cliproxyauth.Auth{
		Provider: "openai-compatible-openrouter-batch",
		Attributes: map[string]string{
			"base_url": server.URL + "/api/v1",
			"api_key":  "test-key",
		},
	}
	resp, err := exec.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "anthropic/claude-opus-5.5:batch",
		Payload: []byte(`{"model":"anthropic/claude-opus-5.5:batch","messages":[{"role":"user","content":"hi"}]}`),
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai")})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if gjson.GetBytes(resp.Payload, "choices.0.message.content").String() != "huptier" {
		t.Fatalf("completion = %s", string(resp.Payload))
	}
	if polls < 2 {
		t.Fatalf("polls = %d, want a retry after the first 404", polls)
	}
}

func TestOpenRouterBatchExecutorFlattensTopLevelToolOneOf(t *testing.T) {
	var submitBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			submitBody, _ = io.ReadAll(r.Body)
			customID := gjson.GetBytes(submitBody, "requests.0.custom_id").String()
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"batch_tools","object":"batch","status":"completed","results":[{
				"custom_id":"` + customID + `",
				"response":{"status_code":200,"body":{"id":"gen-1","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"HUPTIER"}}]}},
				"error":null
			}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"batch_tools","status":"completed","results":[]}`))
	}))
	defer server.Close()

	exec := NewOpenRouterBatchExecutor("openai-compatible-openrouter-batch", &config.Config{})
	auth := &cliproxyauth.Auth{
		Provider: "openai-compatible-openrouter-batch",
		Attributes: map[string]string{
			"base_url": server.URL + "/api/v1",
			"api_key":  "test-key",
		},
	}
	payload := []byte(`{
		"model":"anthropic/claude-opus-5.5:batch",
		"messages":[{"role":"user","content":"hi"}],
		"tools":[{
			"type":"function",
			"function":{
				"name":"use_tool",
				"parameters":{
					"type":"object",
					"properties":{
						"tool_name":{"type":"string"},
						"tool_input":{"type":"object"},
						"file":{"type":"string"}
					},
					"oneOf":[
						{"required":["tool_name","tool_input"]},
						{"required":["file"]}
					]
				}
			}
		}]
	}`)
	resp, err := exec.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "anthropic/claude-opus-5.5:batch",
		Payload: payload,
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai")})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	schema := gjson.GetBytes(submitBody, "requests.0.body.tools.0.function.parameters")
	if schema.Get("oneOf").Exists() || schema.Get("anyOf").Exists() || schema.Get("allOf").Exists() {
		t.Fatalf("top-level union survived: %s", schema.Raw)
	}
	if schema.Get("properties.tool_name").String() == "" && !schema.Get("properties.tool_name").Exists() {
		t.Fatalf("properties dropped: %s", schema.Raw)
	}
	if !strings.Contains(schema.Get("description").String(), "tool_name and tool_input") {
		t.Fatalf("description = %s", schema.Get("description").String())
	}
	if gjson.GetBytes(resp.Payload, "choices.0.message.content").String() != "HUPTIER" {
		t.Fatalf("completion = %s", string(resp.Payload))
	}
}

func TestOpenRouterBatchHelpers(t *testing.T) {
	if got := openRouterBatchUpstreamModel("anthropic/claude-opus-5.5:batch"); got != "anthropic/claude-opus-5.5" {
		t.Fatalf("upstream model = %q", got)
	}
	if got := openRouterBatchCollectionURL("https://openrouter.ai/api/v1"); got != "https://openrouter.ai/api/v1/batches" {
		t.Fatalf("collection = %q", got)
	}
	if got := openRouterBatchCollectionURL("https://openrouter.ai/api/v1/batches/"); got != "https://openrouter.ai/api/v1/batches" {
		t.Fatalf("collection with suffix = %q", got)
	}
	if !IsOpenRouterBatchProvider("openai-compatible-openrouter-batch") {
		t.Fatal("expected provider key to select the batch executor")
	}
	if IsOpenRouterBatchProvider("openai-compatible-openrouter") {
		t.Fatal("realtime openrouter provider must stay on chat completions")
	}
}
