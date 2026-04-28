package openai

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func appendUsageWarningToOpenAIResponse(payload []byte, warning string) []byte {
	warning = strings.TrimSpace(warning)
	if warning == "" || !json.Valid(payload) {
		return payload
	}
	appendText := "\n\n" + warning

	if content := gjson.GetBytes(payload, "choices.0.message.content"); content.Exists() && content.Type == gjson.String {
		updated, err := sjson.SetBytes(payload, "choices.0.message.content", content.String()+appendText)
		if err == nil {
			return updated
		}
	}
	if text := gjson.GetBytes(payload, "choices.0.text"); text.Exists() && text.Type == gjson.String {
		updated, err := sjson.SetBytes(payload, "choices.0.text", text.String()+appendText)
		if err == nil {
			return updated
		}
	}
	return payload
}

func buildChatCompletionUsageWarningChunk(modelName string, warning string) []byte {
	warning = strings.TrimSpace(warning)
	if warning == "" {
		return nil
	}
	payload := map[string]any{
		"id":      "usage-limit-warning",
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   strings.TrimSpace(modelName),
		"choices": []map[string]any{
			{
				"index": 0,
				"delta": map[string]string{
					"content": "\n\n" + warning,
				},
				"finish_reason": nil,
			},
		},
	}
	data, _ := json.Marshal(payload)
	return data
}

func buildCompletionUsageWarningChunk(modelName string, warning string) []byte {
	warning = strings.TrimSpace(warning)
	if warning == "" {
		return nil
	}
	payload := map[string]any{
		"id":      "usage-limit-warning",
		"object":  "text_completion",
		"created": time.Now().Unix(),
		"model":   strings.TrimSpace(modelName),
		"choices": []map[string]any{
			{
				"index":         0,
				"text":          "\n\n" + warning,
				"finish_reason": nil,
			},
		},
	}
	data, _ := json.Marshal(payload)
	return data
}
