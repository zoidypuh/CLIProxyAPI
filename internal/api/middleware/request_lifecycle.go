package middleware

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/usage"
)

type lifecycleRequestMetadata struct {
	model           string
	sessionID       string
	stream          bool
	messageCount    int
	maxOutputTokens int64
}

func publishRequestStartedLifecycleEvent(c *gin.Context, info *RequestInfo) {
	if info == nil {
		return
	}
	meta := requestLifecycleMetadata(c, info.Body)
	usage.PublishRequestLifecycleEvent(usage.RequestLifecycleEvent{
		Event:           usage.RequestLifecycleStarted,
		Timestamp:       info.Timestamp,
		RequestID:       info.RequestID,
		LogFile:         info.LogFile,
		Method:          info.Method,
		Endpoint:        info.URL,
		Model:           meta.model,
		Source:          requestSource(c),
		SessionID:       meta.sessionID,
		Stream:          meta.stream,
		MessageCount:    meta.messageCount,
		MaxOutputTokens: meta.maxOutputTokens,
		StartedAt:       info.Timestamp,
	})
}

func publishRequestCompletedLifecycleEvent(c *gin.Context, info *RequestInfo, statusCode int, tokens *usage.TokenStats) {
	if info == nil {
		return
	}
	completedAt := time.Now()
	meta := requestLifecycleMetadata(c, info.Body)
	failed := statusCode >= http.StatusBadRequest
	usage.PublishRequestLifecycleEvent(usage.RequestLifecycleEvent{
		Event:           usage.RequestLifecycleCompleted,
		Timestamp:       completedAt,
		RequestID:       info.RequestID,
		LogFile:         info.LogFile,
		Method:          info.Method,
		Endpoint:        info.URL,
		Model:           meta.model,
		Source:          requestSource(c),
		SessionID:       meta.sessionID,
		Stream:          meta.stream,
		MessageCount:    meta.messageCount,
		MaxOutputTokens: meta.maxOutputTokens,
		StartedAt:       info.Timestamp,
		CompletedAt:     completedAt,
		DurationMs:      maxInt64(0, completedAt.Sub(info.Timestamp).Milliseconds()),
		StatusCode:      statusCode,
		Failed:          failed,
		Tokens:          tokens,
	})
}

func requestLifecycleMetadata(c *gin.Context, body []byte) lifecycleRequestMetadata {
	meta := lifecycleRequestMetadata{}
	var payload map[string]any
	if len(body) > 0 && json.Unmarshal(body, &payload) == nil {
		meta.model = stringField(payload, "model")
		meta.stream = boolField(payload, "stream")
		meta.messageCount = listLengthField(payload, "messages")
		if meta.messageCount == 0 {
			meta.messageCount = listLengthField(payload, "contents")
		}
		meta.maxOutputTokens = firstInt64Field(payload, "max_tokens", "max_output_tokens", "max_completion_tokens")
		meta.sessionID = firstStringField(payload, "session_id", "conversation_id", "prompt_cache_key", "previous_response_id")
		if meta.sessionID == "" {
			if metadata, ok := payload["metadata"].(map[string]any); ok {
				meta.sessionID = firstStringField(metadata, "session_id", "conversation_id", "user_id")
			}
		}
	}
	if meta.sessionID == "" && c != nil && c.Request != nil {
		meta.sessionID = sessionIDFromHeaders(c.Request.Header)
	}
	return meta
}

func requestSource(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	return strings.TrimSpace(c.Request.UserAgent())
}

func sessionIDFromHeaders(headers http.Header) string {
	for _, key := range []string{"Session_id", "Session-Id", "X-Session-ID", "X-Client-Request-Id", "Conversation-Id"} {
		if value := strings.TrimSpace(headers.Get(key)); value != "" {
			if strings.EqualFold(key, "Session_id") && !strings.HasPrefix(strings.ToLower(value), "codex:") {
				return "codex:" + value
			}
			return value
		}
	}
	return ""
}

func stringField(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}

func firstStringField(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringField(payload, key); value != "" {
			return value
		}
	}
	return ""
}

func boolField(payload map[string]any, key string) bool {
	value, _ := payload[key].(bool)
	return value
}

func listLengthField(payload map[string]any, key string) int {
	items, _ := payload[key].([]any)
	return len(items)
}

func firstInt64Field(payload map[string]any, keys ...string) int64 {
	for _, key := range keys {
		switch value := payload[key].(type) {
		case float64:
			if value > 0 {
				return int64(value)
			}
		case int64:
			if value > 0 {
				return value
			}
		case int:
			if value > 0 {
				return int64(value)
			}
		}
	}
	return 0
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
