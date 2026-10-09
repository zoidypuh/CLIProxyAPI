package executor

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"
)

// OpenRouter does not implement the OpenAI /images/generations + /images/edits pair.
// It exposes a single JSON endpoint (POST /api/v1/images) that takes reference images
// as `input_references` (data URLs) and returns an OpenAI-style {"data":[{"b64_json":...}]}
// body. Forwarding an OpenAI multipart /images/edits request verbatim yields a bare
// Cloudflare 404, so OpenAI-compatible providers pointing at openrouter.ai are translated here.
const openRouterImagesPath = "/images"

var openRouterPixelSizeRE = regexp.MustCompile(`^\d{2,5}x\d{2,5}$`)

// isOpenRouterImagesBaseURL reports whether the provider base URL is OpenRouter.
func isOpenRouterImagesBaseURL(baseURL string) bool {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "openrouter.ai" || strings.HasSuffix(host, ".openrouter.ai")
}

// openRouterImageField holds one OpenAI images form/JSON field before conversion.
type openRouterImageFields struct {
	values map[string]any
	refs   []string
	mask   bool
}

// prepareOpenRouterImagesPayload converts an OpenAI images request (JSON generations or
// multipart edits) into an OpenRouter /images JSON request.
func prepareOpenRouterImagesPayload(payload []byte, model string, contentType string, stream bool) ([]byte, error) {
	fields := openRouterImageFields{values: map[string]any{}}
	if json.Valid(payload) {
		if err := json.Unmarshal(payload, &fields.values); err != nil {
			return nil, fmt.Errorf("parse images request: %w", err)
		}
		fields.refs = openRouterJSONImageRefs(fields.values)
		if _, ok := fields.values["mask"]; ok {
			fields.mask = true
		}
	} else {
		mediaType, params, errParse := mime.ParseMediaType(strings.TrimSpace(contentType))
		if errParse != nil || !strings.HasPrefix(strings.ToLower(mediaType), "multipart/") {
			return nil, fmt.Errorf("unsupported images request content type %q", contentType)
		}
		boundary := strings.TrimSpace(params["boundary"])
		if boundary == "" {
			return nil, fmt.Errorf("multipart boundary is missing")
		}
		if err := readOpenRouterMultipartImages(payload, boundary, &fields); err != nil {
			return nil, err
		}
	}
	if fields.mask {
		return nil, statusErr{code: http.StatusBadRequest, msg: `{"error":{"message":"OpenRouter's image API does not support masks; disconnect the mask input and describe the edit region in the prompt instead","type":"invalid_request_error","code":"mask_not_supported"}}`}
	}

	out := map[string]any{}
	if m := strings.TrimSpace(model); m != "" {
		out["model"] = m
	} else if m, ok := fields.values["model"].(string); ok {
		out["model"] = m
	}
	prompt, _ := fields.values["prompt"].(string)
	out["prompt"] = prompt

	// String parameters OpenRouter understands natively.
	for _, key := range []string{"quality", "background", "output_format", "aspect_ratio", "resolution", "user", "session_id"} {
		if s := openRouterString(fields.values[key]); s != "" {
			out[key] = s
		}
	}
	// Integer parameters (multipart sends them as strings).
	for _, key := range []string{"n", "seed", "output_compression"} {
		if v, ok := openRouterInt(fields.values[key]); ok {
			out[key] = v
		}
	}
	// size: "auto" means "let the provider choose" -> omit; tiers/pixels pass through.
	if size := openRouterString(fields.values["size"]); size != "" && !strings.EqualFold(size, "auto") {
		if openRouterPixelSizeRE.MatchString(strings.ToLower(size)) {
			out["size"] = strings.ToLower(size)
		} else {
			out["size"] = size
		}
		delete(out, "aspect_ratio")
		delete(out, "resolution")
	}
	if provider, ok := fields.values["provider"].(map[string]any); ok {
		out["provider"] = provider
	}
	// moderation is an OpenAI passthrough option on OpenRouter (provider.options.openai).
	if moderation := openRouterString(fields.values["moderation"]); moderation != "" && strings.HasPrefix(strings.ToLower(model), "openai/") {
		provider, _ := out["provider"].(map[string]any)
		if provider == nil {
			provider = map[string]any{}
		}
		options, _ := provider["options"].(map[string]any)
		if options == nil {
			options = map[string]any{}
		}
		openaiOpts, _ := options["openai"].(map[string]any)
		if openaiOpts == nil {
			openaiOpts = map[string]any{}
		}
		if _, exists := openaiOpts["moderation"]; !exists {
			openaiOpts["moderation"] = moderation
		}
		options["openai"] = openaiOpts
		provider["options"] = options
		out["provider"] = provider
	}
	if len(fields.refs) > 0 {
		refs := make([]map[string]any, 0, len(fields.refs))
		for _, ref := range fields.refs {
			refs = append(refs, map[string]any{"type": "image_url", "image_url": map[string]any{"url": ref}})
		}
		out["input_references"] = refs
	}
	if stream {
		out["stream"] = true
	}
	// response_format is dropped on purpose: OpenRouter always returns b64_json.
	return json.Marshal(out)
}

func openRouterString(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case json.Number:
		return t.String()
	}
	return ""
}

func openRouterInt(v any) (int64, bool) {
	switch t := v.(type) {
	case float64:
		return int64(t), true
	case json.Number:
		i, err := t.Int64()
		return i, err == nil
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		return i, err == nil
	}
	return 0, false
}

// openRouterJSONImageRefs collects reference images from JSON bodies
// (OpenAI-style "images":[{"image_url":...}] / "image" strings, or native input_references).
func openRouterJSONImageRefs(values map[string]any) []string {
	var refs []string
	add := func(v any) {
		switch t := v.(type) {
		case string:
			if s := strings.TrimSpace(t); s != "" {
				refs = append(refs, s)
			}
		case map[string]any:
			if s, ok := t["image_url"].(string); ok && s != "" {
				refs = append(refs, s)
			} else if m, ok := t["image_url"].(map[string]any); ok {
				if s, ok := m["url"].(string); ok && s != "" {
					refs = append(refs, s)
				}
			} else if s, ok := t["url"].(string); ok && s != "" {
				refs = append(refs, s)
			}
		}
	}
	for _, key := range []string{"input_references", "images", "image"} {
		switch t := values[key].(type) {
		case []any:
			for _, item := range t {
				add(item)
			}
		default:
			add(t)
		}
		delete(values, key)
	}
	return refs
}

func readOpenRouterMultipartImages(payload []byte, boundary string, fields *openRouterImageFields) error {
	reader := multipart.NewReader(bytes.NewReader(payload), boundary)
	form, errRead := reader.ReadForm(openAICompatMultipartMemory)
	if errRead != nil {
		return fmt.Errorf("read multipart form failed: %w", errRead)
	}
	defer func() {
		if errRemove := form.RemoveAll(); errRemove != nil {
			log.Errorf("openai compat executor: remove multipart form files error: %v", errRemove)
		}
	}()
	for key, values := range form.Value {
		if len(values) > 0 {
			fields.values[key] = values[len(values)-1]
		}
	}
	for key, files := range form.File {
		lower := strings.ToLower(key)
		if lower == "mask" {
			if len(files) > 0 {
				fields.mask = true
			}
			continue
		}
		if !strings.HasPrefix(lower, "image") {
			continue
		}
		for _, fh := range files {
			if fh == nil {
				continue
			}
			src, errOpen := fh.Open()
			if errOpen != nil {
				return fmt.Errorf("open upload file failed: %w", errOpen)
			}
			data, errCopy := io.ReadAll(src)
			_ = src.Close()
			if errCopy != nil {
				return fmt.Errorf("read upload file failed: %w", errCopy)
			}
			mediaType := strings.TrimSpace(fh.Header.Get("Content-Type"))
			if mediaType == "" || mediaType == "application/octet-stream" {
				mediaType = http.DetectContentType(data)
			}
			fields.refs = append(fields.refs, "data:"+mediaType+";base64,"+base64.StdEncoding.EncodeToString(data))
		}
	}
	return nil
}
