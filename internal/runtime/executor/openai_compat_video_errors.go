package executor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

// explainEmptyOpenAICompatVideoError replaces an upstream video error whose message is empty
// (apikey.fun forwards xAI rejections as {"error":{"message":"","type":"upstream_error"}})
// with a message that carries the HTTP status, the upstream request id and the raw body,
// plus a hint for the most common cause: asking grok-imagine-video-1.5 for 1080p while
// sending last_frame/keyframes/reference_images, which xAI treats as reference-to-video
// and caps at 720p.
func explainEmptyOpenAICompatVideoError(status int, headers http.Header, body []byte, payload []byte) []byte {
	trimmed := strings.TrimSpace(string(body))
	message := ""
	errType := "upstream_error"
	if gjson.Valid(trimmed) {
		root := gjson.Parse(trimmed)
		message = strings.TrimSpace(root.Get("error.message").String())
		if message == "" {
			message = strings.TrimSpace(root.Get("message").String())
		}
		if t := strings.TrimSpace(root.Get("error.type").String()); t != "" {
			errType = t
		}
	} else {
		message = trimmed
	}
	if message != "" {
		return body
	}

	parts := []string{fmt.Sprintf("upstream returned HTTP %d %s with an empty error message", status, http.StatusText(status))}
	for _, key := range []string{"X-Request-Id", "X-Client-Request-Id"} {
		if v := strings.TrimSpace(headers.Get(key)); v != "" {
			parts = append(parts, fmt.Sprintf("%s=%s", strings.ToLower(key), v))
		}
	}
	if raw := trimmed; raw != "" {
		if len(raw) > 300 {
			raw = raw[:300] + "..."
		}
		parts = append(parts, "raw body: "+raw)
	}
	if hint := openAICompatVideoErrorHint(payload); hint != "" {
		parts = append(parts, "likely cause: "+hint)
	}
	out, errMarshal := json.Marshal(map[string]any{
		"error": map[string]any{
			"message": strings.Join(parts, "; "),
			"type":    errType,
			"code":    status,
		},
	})
	if errMarshal != nil {
		return body
	}
	return out
}

func openAICompatVideoErrorHint(payload []byte) string {
	if len(payload) == 0 || !gjson.ValidBytes(payload) {
		return ""
	}
	root := gjson.ParseBytes(payload)
	resolution := strings.ToLower(strings.TrimSpace(root.Get("resolution").String()))
	var refs []string
	for _, key := range []string{"last_frame", "keyframes", "reference_images", "reference_audios"} {
		if v := root.Get(key); v.Exists() && v.Type != gjson.Null {
			refs = append(refs, key)
		}
	}
	if resolution == "1080p" && len(refs) > 0 {
		return fmt.Sprintf("1080p was requested together with %s; grok-imagine-video-1.5 treats image + %s as reference-to-video, which is capped at 720p (use 720p, or drop %s for native 1080p image-to-video)",
			strings.Join(refs, "/"), strings.Join(refs, "/"), strings.Join(refs, "/"))
	}
	return ""
}
