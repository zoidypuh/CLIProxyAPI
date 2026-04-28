package openai

import (
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestAppendUsageWarningToChatCompletion(t *testing.T) {
	payload := []byte(`{"choices":[{"message":{"content":"hello"}}]}`)
	updated := appendUsageWarningToOpenAIResponse(payload, "Warning nearing usage limit")
	content := gjson.GetBytes(updated, "choices.0.message.content").String()
	if !strings.Contains(content, "hello\n\nWarning nearing usage limit") {
		t.Fatalf("content = %q", content)
	}
}

func TestAppendUsageWarningToCompletion(t *testing.T) {
	payload := []byte(`{"choices":[{"text":"hello"}]}`)
	updated := appendUsageWarningToOpenAIResponse(payload, "Warning nearing usage limit")
	text := gjson.GetBytes(updated, "choices.0.text").String()
	if !strings.Contains(text, "hello\n\nWarning nearing usage limit") {
		t.Fatalf("text = %q", text)
	}
}

func TestBuildChatCompletionUsageWarningChunk(t *testing.T) {
	chunk := buildChatCompletionUsageWarningChunk("gpt-test", "Warning nearing usage limit")
	content := gjson.GetBytes(chunk, "choices.0.delta.content").String()
	if !strings.Contains(content, "Warning nearing usage limit") {
		t.Fatalf("content = %q", content)
	}
}
