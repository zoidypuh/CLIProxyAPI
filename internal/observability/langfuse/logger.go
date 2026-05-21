package langfuse

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

const (
	defaultBaseURL  = "https://cloud.langfuse.com"
	defaultMaxChars = 12000
)

// Logger sends completed proxy request cycles to Langfuse.
type Logger struct {
	enabled     bool
	baseURL     string
	publicKey   string
	secretKey   string
	environment string
	release     string
	maxChars    int
	client      *http.Client
}

// NewLogger builds a fail-open Langfuse logger from config and environment.
func NewLogger(cfg config.LangfuseConfig) *Logger {
	if !cfg.Enabled {
		return &Logger{}
	}

	envFile := readEnvFile(cfg.EnvFile)
	baseURL := firstNonEmpty(
		cfg.BaseURL,
		os.Getenv("LANGFUSE_HOST"),
		os.Getenv("LANGFUSE_BASE_URL"),
		os.Getenv("HERMES_LANGFUSE_BASE_URL"),
		envFile["LANGFUSE_HOST"],
		envFile["LANGFUSE_BASE_URL"],
		envFile["HERMES_LANGFUSE_BASE_URL"],
		defaultBaseURL,
	)
	publicKey := firstNonEmpty(cfg.PublicKey, os.Getenv("LANGFUSE_PUBLIC_KEY"), os.Getenv("HERMES_LANGFUSE_PUBLIC_KEY"), envFile["LANGFUSE_PUBLIC_KEY"], envFile["HERMES_LANGFUSE_PUBLIC_KEY"])
	secretKey := firstNonEmpty(cfg.SecretKey, os.Getenv("LANGFUSE_SECRET_KEY"), os.Getenv("HERMES_LANGFUSE_SECRET_KEY"), envFile["LANGFUSE_SECRET_KEY"], envFile["HERMES_LANGFUSE_SECRET_KEY"])
	if strings.TrimSpace(publicKey) == "" || strings.TrimSpace(secretKey) == "" {
		log.Warn("langfuse tracing enabled but credentials are missing; tracing disabled")
		return &Logger{}
	}

	maxChars := cfg.MaxChars
	if maxChars <= 0 {
		maxChars = defaultMaxChars
	}

	return &Logger{
		enabled:     true,
		baseURL:     strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		publicKey:   strings.TrimSpace(publicKey),
		secretKey:   strings.TrimSpace(secretKey),
		environment: firstNonEmpty(cfg.Environment, os.Getenv("LANGFUSE_ENV"), os.Getenv("HERMES_LANGFUSE_ENV"), envFile["LANGFUSE_ENV"], envFile["HERMES_LANGFUSE_ENV"]),
		release:     firstNonEmpty(cfg.Release, os.Getenv("LANGFUSE_RELEASE"), os.Getenv("HERMES_LANGFUSE_RELEASE"), envFile["LANGFUSE_RELEASE"], envFile["HERMES_LANGFUSE_RELEASE"]),
		maxChars:    maxChars,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

func readEnvFile(path string) map[string]string {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	values, err := godotenv.Read(path)
	if err != nil {
		log.WithError(err).WithField("path", path).Warn("failed to read langfuse env file")
		return nil
	}
	return values
}

func (l *Logger) IsEnabled() bool {
	return l != nil && l.enabled
}

func (l *Logger) LogRequest(url, method string, requestHeaders map[string][]string, body []byte, statusCode int, responseHeaders map[string][]string, response, websocketTimeline, apiRequest, apiResponse, apiWebsocketTimeline []byte, apiResponseErrors []*interfaces.ErrorMessage, requestID string, requestTimestamp, apiResponseTimestamp time.Time) error {
	if !l.IsEnabled() {
		return nil
	}
	if requestTimestamp.IsZero() {
		requestTimestamp = time.Now()
	}
	end := time.Now()
	if !apiResponseTimestamp.IsZero() {
		end = apiResponseTimestamp
	}
	traceID := traceID(requestID)
	generationID := observationID(requestID)

	metadata := l.metadata(url, method, requestHeaders, responseHeaders, statusCode, requestID, websocketTimeline, apiRequest, apiResponse, apiWebsocketTimeline, apiResponseErrors)
	model := modelFromPayload(body, apiRequest)
	input := l.payloadForLangfuse(body)
	output := l.payloadForLangfuse(response)
	if len(apiResponse) > 0 {
		output = l.payloadForLangfuse(apiResponse)
	}
	usage := usageFromPayload(response, apiResponse)

	batch := []ingestionEvent{
		{
			ID:        uuid.NewString(),
			Timestamp: formatTime(requestTimestamp),
			Type:      "trace-create",
			Body: map[string]any{
				"id":          traceID,
				"timestamp":   formatTime(requestTimestamp),
				"name":        traceName(method, url, requestHeaders, body, apiRequest),
				"input":       input,
				"output":      output,
				"sessionId":   sessionID(requestHeaders, body),
				"release":     emptyToNil(l.release),
				"environment": emptyToNil(l.environment),
				"metadata":    metadata,
				"tags":        []string{"cliproxy", "proxy"},
			},
		},
		{
			ID:        uuid.NewString(),
			Timestamp: formatTime(end),
			Type:      "generation-create",
			Body: map[string]any{
				"id":          generationID,
				"traceId":     traceID,
				"name":        "cliproxy upstream request",
				"startTime":   formatTime(requestTimestamp),
				"endTime":     formatTime(end),
				"model":       emptyToNil(model),
				"input":       input,
				"output":      output,
				"usage":       usage,
				"metadata":    metadata,
				"level":       levelForStatus(statusCode, apiResponseErrors),
				"environment": emptyToNil(l.environment),
			},
		},
	}

	return l.ingest(batch)
}

func (l *Logger) LogStreamingRequest(url, method string, headers map[string][]string, body []byte, requestID string) (logging.StreamingLogWriter, error) {
	if !l.IsEnabled() {
		return &logging.NoOpStreamingLogWriter{}, nil
	}
	return &streamingWriter{
		logger:     l,
		url:        url,
		method:     method,
		headers:    cloneHeaders(headers),
		body:       bytes.Clone(body),
		requestID:  requestID,
		start:      time.Now(),
		statusCode: http.StatusOK,
	}, nil
}

// HandleUsage emits Langfuse traces from the proxy usage stream. Codex websocket
// requests publish usage records per turn, while the HTTP request logger only
// completes when the long-lived websocket closes.
func (l *Logger) HandleUsage(ctx context.Context, record coreusage.Record) {
	if !l.IsEnabled() || !shouldCaptureUsageRecord(record) {
		return
	}
	if record.RequestedAt.IsZero() {
		record.RequestedAt = time.Now()
	}
	end := record.RequestedAt.Add(record.Latency)
	if record.Latency <= 0 {
		end = time.Now()
	}
	traceID := usageTraceID(record)
	generationID := "cliproxy-usage-generation-" + uuid.NewString()
	metadata := l.usageMetadata(record)
	usage := usageFromRecord(record.Detail)
	level := "DEFAULT"
	if record.Failed {
		level = "ERROR"
	}

	batch := []ingestionEvent{
		{
			ID:        uuid.NewString(),
			Timestamp: formatTime(record.RequestedAt),
			Type:      "trace-create",
			Body: map[string]any{
				"id":          traceID,
				"timestamp":   formatTime(record.RequestedAt),
				"name":        usageTraceName(record),
				"sessionId":   emptyToNil(record.SessionID),
				"release":     emptyToNil(l.release),
				"environment": emptyToNil(l.environment),
				"metadata":    metadata,
				"tags":        []string{"cliproxy", "proxy", "usage", record.Provider},
			},
		},
		{
			ID:        uuid.NewString(),
			Timestamp: formatTime(end),
			Type:      "generation-create",
			Body: map[string]any{
				"id":          generationID,
				"traceId":     traceID,
				"name":        "cliproxy usage record",
				"startTime":   formatTime(record.RequestedAt),
				"endTime":     formatTime(end),
				"model":       emptyToNil(record.Model),
				"usage":       usage,
				"metadata":    metadata,
				"level":       level,
				"environment": emptyToNil(l.environment),
			},
		},
	}

	if err := l.ingest(batch); err != nil {
		log.WithError(err).Debug("langfuse usage ingestion failed")
	}
}

func (l *Logger) ingest(batch []ingestionEvent) error {
	if !l.IsEnabled() || len(batch) == 0 {
		return nil
	}

	payload := map[string]any{
		"batch": batch,
		"metadata": map[string]any{
			"source": "cliproxyapi",
		},
	}
	raw, errMarshal := json.Marshal(payload)
	if errMarshal != nil {
		return fmt.Errorf("marshal langfuse payload: %w", errMarshal)
	}

	req, errReq := http.NewRequestWithContext(context.Background(), http.MethodPost, l.baseURL+"/api/public/ingestion", bytes.NewReader(raw))
	if errReq != nil {
		return fmt.Errorf("build langfuse request: %w", errReq)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(l.publicKey+":"+l.secretKey)))

	resp, errDo := l.client.Do(req)
	if errDo != nil {
		log.WithError(errDo).Warn("langfuse ingestion request failed")
		return nil
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.WithError(errClose).Warn("failed to close langfuse response body")
		}
	}()
	if resp.StatusCode >= 400 {
		log.WithField("status", resp.StatusCode).Warn("langfuse ingestion returned error")
	}
	return nil
}

func (l *Logger) payloadForLangfuse(raw []byte) any {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	var value any
	if json.Unmarshal(raw, &value) == nil {
		return l.truncateValue(value)
	}
	return l.truncateString(string(raw))
}

func (l *Logger) metadata(url, method string, requestHeaders, responseHeaders map[string][]string, statusCode int, requestID string, websocketTimeline, apiRequest, apiResponse, apiWebsocketTimeline []byte, apiResponseErrors []*interfaces.ErrorMessage) map[string]any {
	metadata := map[string]any{
		"url":              url,
		"method":           method,
		"status_code":      statusCode,
		"request_id":       requestID,
		"request_headers":  maskedHeaders(requestHeaders),
		"response_headers": maskedHeaders(responseHeaders),
	}
	if len(apiRequest) > 0 {
		metadata["upstream_request"] = l.truncateString(string(apiRequest))
	}
	if len(apiResponse) > 0 {
		metadata["upstream_response"] = l.truncateString(string(apiResponse))
	}
	if len(websocketTimeline) > 0 {
		metadata["websocket_timeline"] = l.truncateString(string(websocketTimeline))
	}
	if len(apiWebsocketTimeline) > 0 {
		metadata["api_websocket_timeline"] = l.truncateString(string(apiWebsocketTimeline))
	}
	if len(apiResponseErrors) > 0 {
		errors := make([]map[string]any, 0, len(apiResponseErrors))
		for _, errMsg := range apiResponseErrors {
			if errMsg == nil {
				continue
			}
			item := map[string]any{"status_code": errMsg.StatusCode}
			if errMsg.Error != nil {
				item["error"] = l.truncateString(errMsg.Error.Error())
			}
			errors = append(errors, item)
		}
		metadata["api_response_errors"] = errors
	}
	return metadata
}

func (l *Logger) truncateValue(value any) any {
	switch v := value.(type) {
	case string:
		return l.truncateString(v)
	case []any:
		out := make([]any, len(v))
		for i := range v {
			out[i] = l.truncateValue(v[i])
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = l.truncateValue(item)
		}
		return out
	default:
		return value
	}
}

func (l *Logger) truncateString(value string) string {
	if l.maxChars <= 0 || len(value) <= l.maxChars {
		return value
	}
	if l.maxChars <= 32 {
		return value[:l.maxChars]
	}
	cut := l.maxChars - len("...[truncated]")
	if cut <= 0 {
		return value[:l.maxChars]
	}
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut] + "...[truncated]"
}

type ingestionEvent struct {
	ID        string         `json:"id"`
	Timestamp string         `json:"timestamp"`
	Type      string         `json:"type"`
	Body      map[string]any `json:"body"`
}

type streamingWriter struct {
	logger              *Logger
	url                 string
	method              string
	headers             map[string][]string
	body                []byte
	requestID           string
	start               time.Time
	firstChunkTimestamp time.Time
	statusCode          int
	responseHeaders     map[string][]string
	apiRequest          []byte
	apiResponse         []byte
	apiWebsocket        []byte
	mu                  sync.Mutex
	response            bytes.Buffer
}

func (w *streamingWriter) WriteChunkAsync(chunk []byte) {
	if w == nil || w.logger == nil || len(chunk) == 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.response.Len() >= w.logger.maxChars {
		return
	}
	remaining := w.logger.maxChars - w.response.Len()
	if len(chunk) > remaining {
		chunk = chunk[:remaining]
	}
	w.response.Write(chunk)
}

func (w *streamingWriter) WriteStatus(status int, headers map[string][]string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.statusCode = status
	w.responseHeaders = cloneHeaders(headers)
	return nil
}

func (w *streamingWriter) WriteAPIRequest(apiRequest []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.apiRequest = bytes.Clone(apiRequest)
	return nil
}

func (w *streamingWriter) WriteAPIResponse(apiResponse []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.apiResponse = bytes.Clone(apiResponse)
	return nil
}

func (w *streamingWriter) WriteAPIWebsocketTimeline(apiWebsocketTimeline []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.apiWebsocket = bytes.Clone(apiWebsocketTimeline)
	return nil
}

func (w *streamingWriter) SetFirstChunkTimestamp(timestamp time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.firstChunkTimestamp = timestamp
}

func (w *streamingWriter) Close() error {
	if w == nil || w.logger == nil {
		return nil
	}
	w.mu.Lock()
	response := bytes.Clone(w.response.Bytes())
	apiRequest := bytes.Clone(w.apiRequest)
	apiResponse := bytes.Clone(w.apiResponse)
	apiWebsocket := bytes.Clone(w.apiWebsocket)
	responseHeaders := cloneHeaders(w.responseHeaders)
	statusCode := w.statusCode
	start := w.start
	requestID := w.requestID
	url := w.url
	method := w.method
	headers := cloneHeaders(w.headers)
	body := bytes.Clone(w.body)
	w.mu.Unlock()

	return w.logger.LogRequest(url, method, headers, body, statusCode, responseHeaders, response, nil, apiRequest, apiResponse, apiWebsocket, nil, requestID, start, time.Now())
}

func traceName(method, url string, headers map[string][]string, body []byte, apiRequest []byte) string {
	if name := friendlyTraceName(headers, body, apiRequest); name != "" {
		return name
	}
	method = strings.TrimSpace(method)
	if method == "" {
		method = "REQUEST"
	}
	path := strings.TrimSpace(strings.Split(url, "?")[0])
	if path == "" {
		path = "/"
	}
	return "cliproxy " + method + " " + path
}

func friendlyTraceName(headers map[string][]string, body []byte, apiRequest []byte) string {
	parts := make([]string, 0, 3)
	if label := friendlySessionName(headers, body, apiRequest); label != "" {
		parts = append(parts, label)
	}
	if model := modelFromPayload(body, apiRequest); model != "" {
		parts = append(parts, model)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " / ")
}

func friendlySessionName(headers map[string][]string, body []byte, apiRequest []byte) string {
	for _, key := range []string{"Authorization", "X-Api-Key", "Api-Key"} {
		if value := localSessionLabel(firstHeader(headers, key)); value != "" {
			return value
		}
	}
	if label := displaySessionLabel(sessionID(headers, body)); label != "" {
		return label
	}
	if provider := providerFromAPIRequest(apiRequest); provider != "" {
		return provider
	}
	if userAgent := compactUserAgent(firstHeader(headers, "User-Agent")); userAgent != "" {
		return userAgent
	}
	return ""
}

func localSessionLabel(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(strings.ToLower(value), "bearer ") {
		value = strings.TrimSpace(value[len("bearer "):])
	}
	if value == "" || looksTokenLike(value) {
		return ""
	}
	lower := strings.ToLower(value)
	if strings.Contains(lower, "secret") || strings.Contains(lower, "token") || strings.Contains(lower, "key") {
		return ""
	}
	switch lower {
	case "he...es":
		return "hermes"
	case "ho...ho":
		return "honcho"
	case "qw...te", "qwen...gate":
		return "qwen-delegate"
	default:
		return value
	}
}

func displaySessionLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if looksLikeUUID(value) {
		return "codex"
	}
	return localSessionLabel(value)
}

func looksLikeUUID(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 36 {
		return false
	}
	for i, char := range value {
		switch i {
		case 8, 13, 18, 23:
			if char != '-' {
				return false
			}
		default:
			if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
				return false
			}
		}
	}
	return true
}

func looksTokenLike(value string) bool {
	stripped := strings.Join(strings.Fields(value), "")
	lower := strings.ToLower(stripped)
	return len(stripped) > 80 ||
		strings.HasPrefix(lower, "eyj") ||
		strings.HasPrefix(lower, "sk-") ||
		strings.HasPrefix(lower, "sess-") ||
		strings.HasPrefix(lower, "nvapi-") ||
		strings.HasPrefix(lower, "aiza") ||
		strings.HasPrefix(lower, "venice_") ||
		strings.Count(stripped, ".") >= 2
}

func providerFromAPIRequest(apiRequest []byte) string {
	text := string(apiRequest)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Auth:") {
			continue
		}
		fields := strings.FieldsFunc(line, func(r rune) bool {
			return r == ' ' || r == ',' || r == '\t'
		})
		for _, field := range fields {
			if value, ok := strings.CutPrefix(field, "provider="); ok {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}

func compactUserAgent(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" {
		return ""
	}
	if len(value) <= 52 {
		return value
	}
	return value[:49] + "..."
}

func traceID(requestID string) string {
	if strings.TrimSpace(requestID) == "" {
		return uuid.NewString()
	}
	return "cliproxy-" + strings.TrimSpace(requestID)
}

func observationID(requestID string) string {
	if strings.TrimSpace(requestID) == "" {
		return uuid.NewString()
	}
	return "cliproxy-generation-" + strings.TrimSpace(requestID)
}

func sessionID(headers map[string][]string, body []byte) string {
	for _, key := range []string{"X-Session-ID", "Session_id", "X-Amp-Thread-Id", "X-Client-Request-Id"} {
		if value := firstHeader(headers, key); value != "" {
			return value
		}
	}
	for _, path := range []string{"metadata.user_id", "conversation_id", "session_id", "previous_response_id"} {
		if value := strings.TrimSpace(gjson.GetBytes(body, path).String()); value != "" {
			return value
		}
	}
	return ""
}

func modelFromPayload(bodies ...[]byte) string {
	for _, body := range bodies {
		if model := strings.TrimSpace(gjson.GetBytes(body, "model").String()); model != "" {
			return model
		}
	}
	return ""
}

func usageFromPayload(bodies ...[]byte) map[string]any {
	for _, body := range bodies {
		if usage := gjson.GetBytes(body, "usage"); usage.Exists() && usage.Type == gjson.JSON {
			return usageMap(usage)
		}
		if usage := gjson.GetBytes(body, "usageMetadata"); usage.Exists() && usage.Type == gjson.JSON {
			return map[string]any{
				"promptTokens":     usage.Get("promptTokenCount").Int(),
				"completionTokens": usage.Get("candidatesTokenCount").Int(),
				"totalTokens":      usage.Get("totalTokenCount").Int(),
			}
		}
	}
	return nil
}

func usageMap(result gjson.Result) map[string]any {
	usage := map[string]any{}
	if v := result.Get("prompt_tokens"); v.Exists() {
		usage["promptTokens"] = v.Int()
	}
	if v := result.Get("input_tokens"); v.Exists() {
		usage["promptTokens"] = v.Int()
	}
	if v := result.Get("completion_tokens"); v.Exists() {
		usage["completionTokens"] = v.Int()
	}
	if v := result.Get("output_tokens"); v.Exists() {
		usage["completionTokens"] = v.Int()
	}
	if v := result.Get("total_tokens"); v.Exists() {
		usage["totalTokens"] = v.Int()
	}
	if len(usage) == 0 {
		return nil
	}
	return usage
}

func shouldCaptureUsageRecord(record coreusage.Record) bool {
	return strings.EqualFold(strings.TrimSpace(record.Provider), "codex")
}

func usageTraceID(record coreusage.Record) string {
	requestID := strings.TrimSpace(record.RequestID)
	if requestID != "" {
		return "cliproxy-usage-" + requestID + "-" + fmt.Sprint(record.RequestedAt.UnixNano())
	}
	return "cliproxy-usage-" + uuid.NewString()
}

func usageTraceName(record coreusage.Record) string {
	parts := make([]string, 0, 2)
	if label := usageSessionLabel(record); label != "" {
		parts = append(parts, label)
	}
	if model := strings.TrimSpace(record.Model); model != "" {
		parts = append(parts, model)
	}
	if len(parts) > 0 {
		return strings.Join(parts, " / ")
	}
	return "cliproxy usage"
}

func usageSessionLabel(record coreusage.Record) string {
	for _, value := range []string{record.APIKey, record.SessionID, record.Provider, record.Source} {
		if label := displaySessionLabel(value); label != "" {
			return label
		}
	}
	return ""
}

func usageFromRecord(detail coreusage.Detail) map[string]any {
	usage := map[string]any{}
	if detail.InputTokens != 0 {
		usage["promptTokens"] = detail.InputTokens
	}
	if detail.OutputTokens != 0 {
		usage["completionTokens"] = detail.OutputTokens
	}
	if detail.TotalTokens != 0 {
		usage["totalTokens"] = detail.TotalTokens
	}
	if detail.CachedTokens != 0 {
		usage["cachedTokens"] = detail.CachedTokens
	}
	if detail.CacheReadTokens != 0 {
		usage["cacheReadTokens"] = detail.CacheReadTokens
	}
	if detail.CacheCreationTokens != 0 {
		usage["cacheCreationTokens"] = detail.CacheCreationTokens
	}
	if detail.ReasoningTokens != 0 {
		usage["reasoningTokens"] = detail.ReasoningTokens
	}
	if len(usage) == 0 {
		return nil
	}
	return usage
}

func (l *Logger) usageMetadata(record coreusage.Record) map[string]any {
	metadata := map[string]any{
		"provider":         record.Provider,
		"model":            record.Model,
		"alias":            record.Alias,
		"source":           record.Source,
		"auth_id":          record.AuthID,
		"auth_index":       record.AuthIndex,
		"auth_type":        record.AuthType,
		"session_id":       record.SessionID,
		"request_id":       record.RequestID,
		"log_file":         record.LogFile,
		"reasoning_effort": record.ReasoningEffort,
		"latency_ms":       record.Latency.Milliseconds(),
		"failed":           record.Failed,
	}
	if record.APIKey != "" {
		metadata["api_key"] = util.HideAPIKey(record.APIKey)
	}
	if record.Fail.StatusCode != 0 {
		metadata["failure_status_code"] = record.Fail.StatusCode
	}
	if record.Fail.Body != "" {
		metadata["failure_body"] = l.truncateString(record.Fail.Body)
	}
	if len(record.ResponseHeaders) > 0 {
		metadata["response_headers"] = maskedHeaders(record.ResponseHeaders)
	}
	return compactMetadata(metadata)
}

func compactMetadata(metadata map[string]any) map[string]any {
	out := make(map[string]any, len(metadata))
	for key, value := range metadata {
		switch v := value.(type) {
		case string:
			if strings.TrimSpace(v) == "" {
				continue
			}
		case int64:
			if v == 0 {
				continue
			}
		case bool:
			if !v {
				continue
			}
		case nil:
			continue
		}
		out[key] = value
	}
	return out
}

func levelForStatus(statusCode int, apiResponseErrors []*interfaces.ErrorMessage) string {
	if statusCode >= http.StatusBadRequest || len(apiResponseErrors) > 0 {
		return "ERROR"
	}
	return "DEFAULT"
}

func maskedHeaders(headers map[string][]string) map[string][]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string][]string, len(headers))
	for key, values := range headers {
		masked := make([]string, 0, len(values))
		for _, value := range values {
			if strings.EqualFold(strings.TrimSpace(key), "Authorization") {
				masked = append(masked, maskAuthorizationForTrace(value))
				continue
			}
			masked = append(masked, util.MaskSensitiveHeaderValue(key, value))
		}
		out[key] = masked
	}
	return out
}

func maskAuthorizationForTrace(value string) string {
	parts := strings.SplitN(strings.TrimSpace(value), " ", 2)
	if len(parts) < 2 {
		return util.HideAPIKey(value)
	}
	return parts[0] + " " + util.HideAPIKey(parts[1])
}

func cloneHeaders(headers map[string][]string) map[string][]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string][]string, len(headers))
	for key, values := range headers {
		out[key] = append([]string(nil), values...)
	}
	return out
}

func firstHeader(headers map[string][]string, key string) string {
	for headerKey, values := range headers {
		if strings.EqualFold(headerKey, key) && len(values) > 0 {
			return strings.TrimSpace(values[0])
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func emptyToNil(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}

func formatTime(ts time.Time) string {
	if ts.IsZero() {
		ts = time.Now()
	}
	return ts.UTC().Format(time.RFC3339Nano)
}
