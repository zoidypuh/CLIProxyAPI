package langfuse

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestLoggerLogRequestSendsLangfuseIngestion(t *testing.T) {
	var gotAuth string
	var gotPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/public/ingestion" {
			t.Fatalf("path = %s, want /api/public/ingestion", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"successes":[],"errors":[]}`))
	}))
	defer server.Close()

	logger := NewLogger(config.LangfuseConfig{
		Enabled:     true,
		BaseURL:     server.URL,
		PublicKey:   "pk-lf-test",
		SecretKey:   "sk-lf-test",
		Environment: "test",
		MaxChars:    12000,
	})
	if !logger.IsEnabled() {
		t.Fatal("logger should be enabled")
	}

	err := logger.LogRequest(
		"/v1/responses",
		http.MethodPost,
		map[string][]string{
			"Authorization": {"Bearer secret-token"},
			"X-Session-ID":  {"session-1"},
		},
		[]byte(`{"model":"gpt-test","input":"hello"}`),
		http.StatusOK,
		map[string][]string{"Content-Type": {"application/json"}},
		[]byte(`{"usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7},"output":"hi"}`),
		nil,
		[]byte("upstream request"),
		[]byte(`{"usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7},"output":"hi"}`),
		nil,
		nil,
		"abcd1234",
		time.Date(2026, 5, 21, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 21, 10, 0, 1, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("LogRequest returned error: %v", err)
	}

	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("pk-lf-test:sk-lf-test"))
	if gotAuth != wantAuth {
		t.Fatalf("auth header = %q, want %q", gotAuth, wantAuth)
	}

	batch, ok := gotPayload["batch"].([]any)
	if !ok || len(batch) != 2 {
		t.Fatalf("batch = %#v, want 2 events", gotPayload["batch"])
	}
	trace := batch[0].(map[string]any)
	if trace["type"] != "trace-create" {
		t.Fatalf("first event type = %v", trace["type"])
	}
	traceBody := trace["body"].(map[string]any)
	if traceBody["id"] != "cliproxy-abcd1234" {
		t.Fatalf("trace id = %v", traceBody["id"])
	}
	if traceBody["sessionId"] != "session-1" {
		t.Fatalf("session id = %v", traceBody["sessionId"])
	}

	generation := batch[1].(map[string]any)
	if generation["type"] != "generation-create" {
		t.Fatalf("second event type = %v", generation["type"])
	}
	generationBody := generation["body"].(map[string]any)
	if generationBody["model"] != "gpt-test" {
		t.Fatalf("generation model = %v", generationBody["model"])
	}
	usage := generationBody["usage"].(map[string]any)
	if usage["promptTokens"].(float64) != 3 || usage["completionTokens"].(float64) != 4 || usage["totalTokens"].(float64) != 7 {
		t.Fatalf("usage = %#v", usage)
	}
	metadata := generationBody["metadata"].(map[string]any)
	headers := metadata["request_headers"].(map[string]any)
	authValues := headers["Authorization"].([]any)
	if strings.Contains(authValues[0].(string), "secret-token") {
		t.Fatalf("authorization header was not masked: %q", authValues[0])
	}
}

func TestLoggerReadsCredentialsFromEnvFile(t *testing.T) {
	envFile := t.TempDir() + "/langfuse.env"
	t.Setenv("LANGFUSE_PUBLIC_KEY", "")
	t.Setenv("LANGFUSE_SECRET_KEY", "")
	if err := os.WriteFile(envFile, []byte("HERMES_LANGFUSE_PUBLIC_KEY=pk-lf-file\nHERMES_LANGFUSE_SECRET_KEY=sk-lf-file\nHERMES_LANGFUSE_BASE_URL=http://example.test\n"), 0600); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	logger := NewLogger(config.LangfuseConfig{
		Enabled: true,
		EnvFile: envFile,
	})
	if !logger.IsEnabled() {
		t.Fatal("logger should be enabled from env file")
	}
	if logger.publicKey != "pk-lf-file" || logger.secretKey != "sk-lf-file" || logger.baseURL != "http://example.test" {
		t.Fatalf("unexpected credentials/base url: %#v", logger)
	}
}

func TestLoggerDisabledWhenCredentialsMissing(t *testing.T) {
	t.Setenv("LANGFUSE_PUBLIC_KEY", "")
	t.Setenv("LANGFUSE_SECRET_KEY", "")
	t.Setenv("HERMES_LANGFUSE_PUBLIC_KEY", "")
	t.Setenv("HERMES_LANGFUSE_SECRET_KEY", "")

	logger := NewLogger(config.LangfuseConfig{Enabled: true, BaseURL: "http://example.test"})
	if logger.IsEnabled() {
		t.Fatal("logger should be disabled without credentials")
	}
}
