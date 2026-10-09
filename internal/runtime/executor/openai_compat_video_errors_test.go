package executor

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestExplainEmptyOpenAICompatVideoErrorAddsStatusAndHint(t *testing.T) {
	headers := http.Header{}
	headers.Set("X-Request-Id", "req-123")
	payload := []byte(`{"model":"grok-imagine-video-1.5","resolution":"1080p","image":{"url":"data:x"},"last_frame":{"url":"data:x"}}`)
	out := explainEmptyOpenAICompatVideoError(400, headers, []byte(`{"error":{"message":"","type":"upstream_error"}}`), payload)
	msg := gjson.GetBytes(out, "error.message").String()
	for _, want := range []string{"HTTP 400", "x-request-id=req-123", "raw body:", "last_frame", "720p"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message %q missing %q", msg, want)
		}
	}
	if gjson.GetBytes(out, "error.type").String() != "upstream_error" {
		t.Fatalf("type not preserved: %s", out)
	}
}

func TestExplainEmptyOpenAICompatVideoErrorKeepsRealMessages(t *testing.T) {
	body := []byte(`{"error":{"message":"bad prompt","type":"invalid_request_error"}}`)
	if out := explainEmptyOpenAICompatVideoError(400, http.Header{}, body, nil); string(out) != string(body) {
		t.Fatalf("real message must pass through unchanged: %s", out)
	}
	out := explainEmptyOpenAICompatVideoError(502, http.Header{}, nil, []byte(`{"resolution":"720p"}`))
	if msg := gjson.GetBytes(out, "error.message").String(); !strings.Contains(msg, "HTTP 502") || strings.Contains(msg, "likely cause") {
		t.Fatalf("unexpected message: %s", msg)
	}
}
