package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// IsOpenRouterBatchProvider reports the openai-compat provider that must use
// OpenRouter POST/GET /api/v1/batches instead of /chat/completions.
func IsOpenRouterBatchProvider(providerKey string) bool {
	return strings.EqualFold(strings.TrimSpace(providerKey), "openai-compatible-"+openRouterBatchProviderName) ||
		strings.EqualFold(strings.TrimSpace(providerKey), openRouterBatchProviderName)
}

const (
	openRouterBatchProviderName = "openrouter-batch"
	openRouterBatchEndpoint     = "/v1/chat/completions"
	openRouterBatchPollInterval = 3 * time.Second
	// Batch jobs are long-running by design. Poll for up to 30 minutes before
	// giving up; OpenRouter's own completion window is 24h.
	openRouterBatchMaxWait = 30 * time.Minute
)

// OpenRouterBatchExecutor serves a synchronous chat completion by submitting one
// OpenRouter Batch API request and polling until the inlined result is ready.
// Batch-only model slugs (for example anthropic/claude-opus-5.5:batch) reject
// POST /chat/completions, so this executor never calls that path.
type OpenRouterBatchExecutor struct {
	*OpenAICompatExecutor
}

// NewOpenRouterBatchExecutor creates the executor bound to the openrouter-batch provider key.
func NewOpenRouterBatchExecutor(provider string, cfg *config.Config) *OpenRouterBatchExecutor {
	return &OpenRouterBatchExecutor{OpenAICompatExecutor: NewOpenAICompatExecutor(provider, cfg)}
}

// Execute submits the translated chat body as a one-item batch and returns the
// completed chat completion in the caller's response format.
func (e *OpenRouterBatchExecutor) Execute(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (resp cliproxyexecutor.Response, err error) {
	ctx = helps.EnsureSessionContext(ctx, opts, req.Payload)
	baseModel := thinking.ParseSuffix(req.Model).ModelName

	reporter := helps.NewExecutorUsageReporter(ctx, e.OpenAICompatExecutor, baseModel, auth)
	defer reporter.TrackFailure(ctx, &err)

	baseURL, apiKey := e.resolveCredentials(auth)
	if baseURL == "" {
		err = statusErr{code: http.StatusUnauthorized, msg: "missing provider baseURL"}
		return resp, err
	}
	if strings.TrimSpace(apiKey) == "" {
		err = statusErr{code: http.StatusUnauthorized, msg: "missing provider api key"}
		return resp, err
	}

	translated, err := e.translateBatchChatBody(ctx, auth, req, opts, false)
	if err != nil {
		return resp, err
	}
	upstreamModel := openRouterBatchUpstreamModel(baseModel)
	translated = e.overrideModel(translated, upstreamModel)
	if updated, errDelete := sjson.DeleteBytes(translated, "stream"); errDelete == nil {
		translated = updated
	}
	reporter.SetTranslatedReasoningEffort(translated, "openai")

	body, headers, err := e.runOpenRouterBatch(ctx, auth, baseURL, apiKey, upstreamModel, translated, opts)
	if err != nil {
		return resp, err
	}
	reporter.Publish(ctx, helps.ParseOpenAIUsage(body))
	reporter.EnsurePublished(ctx)

	responseFormat := cliproxyexecutor.ResponseFormatOrSource(opts)
	to := sdktranslator.FromString("openai")
	var param any
	out := sdktranslator.TranslateNonStream(ctx, to, responseFormat, req.Model, opts.OriginalRequest, translated, body, &param)
	if responseFormat == sdktranslator.FormatOpenAIResponse {
		out = helps.EnsureResponsesUsageDetails(out)
	}
	return cliproxyexecutor.Response{Payload: out, Headers: headers}, nil
}

// ExecuteStream runs the same batch round trip, then emits the completed
// completion as a single downstream SSE frame so streaming clients still finish.
func (e *OpenRouterBatchExecutor) ExecuteStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (_ *cliproxyexecutor.StreamResult, err error) {
	ctx = helps.EnsureSessionContext(ctx, opts, req.Payload)
	baseModel := thinking.ParseSuffix(req.Model).ModelName

	reporter := helps.NewExecutorUsageReporter(ctx, e.OpenAICompatExecutor, baseModel, auth)
	defer reporter.TrackFailure(ctx, &err)

	baseURL, apiKey := e.resolveCredentials(auth)
	if baseURL == "" {
		err = statusErr{code: http.StatusUnauthorized, msg: "missing provider baseURL"}
		return nil, err
	}
	if strings.TrimSpace(apiKey) == "" {
		err = statusErr{code: http.StatusUnauthorized, msg: "missing provider api key"}
		return nil, err
	}

	translated, err := e.translateBatchChatBody(ctx, auth, req, opts, true)
	if err != nil {
		return nil, err
	}
	upstreamModel := openRouterBatchUpstreamModel(baseModel)
	translated = e.overrideModel(translated, upstreamModel)
	if updated, errDelete := sjson.DeleteBytes(translated, "stream"); errDelete == nil {
		translated = updated
	}
	if updated, errDelete := sjson.DeleteBytes(translated, "stream_options"); errDelete == nil {
		translated = updated
	}
	reporter.SetTranslatedReasoningEffort(translated, "openai")

	body, headers, err := e.runOpenRouterBatch(ctx, auth, baseURL, apiKey, upstreamModel, translated, opts)
	if err != nil {
		return nil, err
	}
	reporter.Publish(ctx, helps.ParseOpenAIUsage(body))
	reporter.EnsurePublished(ctx)

	from := opts.SourceFormat
	responseFormat := cliproxyexecutor.ResponseFormatOrSource(opts)
	to := sdktranslator.FromString("openai")
	originalPayload := req.Payload
	if len(opts.OriginalRequest) > 0 {
		originalPayload = opts.OriginalRequest
	}
	out := make(chan cliproxyexecutor.StreamChunk, 8)
	go func() {
		defer close(out)
		claudeInputTokens := helps.NewClaudeInputTokenState(from, to, responseFormat, originalPayload)
		var param any
		streamLine := append([]byte("data: "), body...)
		chunks := helps.TranslateStreamWithClaudeInputTokens(ctx, to, responseFormat, req.Model, opts.OriginalRequest, translated, streamLine, &param, claudeInputTokens)
		doneChunks := helps.TranslateStreamWithClaudeInputTokens(ctx, to, responseFormat, req.Model, opts.OriginalRequest, translated, []byte("data: [DONE]"), &param, claudeInputTokens)
		chunks = append(chunks, doneChunks...)
		for i := range chunks {
			select {
			case out <- cliproxyexecutor.StreamChunk{Payload: chunks[i]}:
			case <-ctx.Done():
				return
			}
		}
	}()
	if headers == nil {
		headers = http.Header{}
	}
	return &cliproxyexecutor.StreamResult{Headers: headers, Chunks: out}, nil
}

func (e *OpenRouterBatchExecutor) translateBatchChatBody(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options, stream bool) ([]byte, error) {
	baseModel := thinking.ParseSuffix(req.Model).ModelName
	from := opts.SourceFormat
	to := sdktranslator.FromString("openai")
	originalPayload := req.Payload
	if len(opts.OriginalRequest) > 0 {
		originalPayload = opts.OriginalRequest
	}
	isCompat := helps.APIKeyModelIsCompat(req)
	originalTranslated := helps.TranslateRequestWithAPIKeyModelCompatibility(ctx, opts.Headers, e.cfg, from, to, baseModel, originalPayload, stream, isCompat)
	translated := helps.TranslateRequestWithAPIKeyModelCompatibility(ctx, opts.Headers, e.cfg, from, to, baseModel, req.Payload, stream, isCompat)

	translated, err := helps.ApplyRequestThinking(translated, req, opts, from.String(), to.String(), e.Identifier())
	if err != nil {
		return nil, err
	}
	requestedModel := helps.PayloadRequestedModel(opts, req.Model)
	requestPath := helps.PayloadRequestPath(opts)
	translated = helps.ApplyPayloadConfigWithRequest(e.cfg, baseModel, to.String(), from.String(), "", translated, originalTranslated, requestedModel, requestPath, opts.Headers)
	if helps.ShouldNormalizeOpenAIToolResultsForModel(e.resolveCompatConfig(auth), baseModel, requestedModel) {
		translated = helps.NormalizeOpenAIToolResultsTextOnly(translated)
	}
	translated, err = e.applyPromptCacheKey(ctx, auth, from, baseModel, req, opts, translated)
	if err != nil {
		return nil, err
	}
	// Anthropic rejects oneOf, anyOf, and allOf on the top level of a tool
	// input_schema. The Grok harness sends use_tool that way.
	return flattenAnthropicTopLevelToolUnions(translated), nil
}

// flattenAnthropicTopLevelToolUnions drops top-level oneOf, anyOf, and allOf
// from each tool's JSON schema. Sibling properties stay. Variant requirements
// are copied into the tool description so the model still sees the alternatives.
func flattenAnthropicTopLevelToolUnions(body []byte) []byte {
	if !gjson.GetBytes(body, "tools").IsArray() {
		return body
	}
	updated := body
	gjson.GetBytes(body, "tools").ForEach(func(index, tool gjson.Result) bool {
		path := "tools." + index.String() + ".function.parameters"
		schema := tool.Get("function.parameters")
		if !schema.Exists() {
			path = "tools." + index.String() + ".parameters"
			schema = tool.Get("parameters")
		}
		if !schema.Exists() {
			return true
		}
		flat, changed := flattenTopLevelUnion(schema)
		if !changed {
			return true
		}
		encoded, errMarshal := json.Marshal(flat)
		if errMarshal != nil {
			return true
		}
		if next, errSet := sjson.SetRawBytes(updated, path, encoded); errSet == nil {
			updated = next
		}
		return true
	})
	return updated
}

func flattenTopLevelUnion(schema gjson.Result) (map[string]any, bool) {
	var raw map[string]any
	if errUnmarshal := json.Unmarshal([]byte(schema.Raw), &raw); errUnmarshal != nil {
		return nil, false
	}
	union, key := topLevelUnion(raw)
	if key == "" {
		return nil, false
	}
	delete(raw, key)
	notes := unionVariantNotes(union)
	if notes != "" {
		existing, _ := raw["description"].(string)
		if existing == "" {
			raw["description"] = notes
		} else if !strings.Contains(existing, notes) {
			raw["description"] = existing + " Alternatives: " + notes
		}
	}
	return raw, true
}

func topLevelUnion(schema map[string]any) ([]any, string) {
	for _, key := range []string{"oneOf", "anyOf", "allOf"} {
		items, ok := schema[key].([]any)
		if ok && len(items) > 0 {
			return items, key
		}
	}
	return nil, ""
}

func unionVariantNotes(union []any) string {
	notes := make([]string, 0, len(union))
	for i, item := range union {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		required, _ := obj["required"].([]any)
		fields := make([]string, 0, len(required))
		for _, field := range required {
			if name, ok := field.(string); ok && name != "" {
				fields = append(fields, name)
			}
		}
		if len(fields) == 0 {
			continue
		}
		notes = append(notes, fmt.Sprintf("option %d requires %s", i+1, strings.Join(fields, " and ")))
	}
	return strings.Join(notes, "; ")
}

func (e *OpenRouterBatchExecutor) runOpenRouterBatch(ctx context.Context, auth *cliproxyauth.Auth, baseURL, apiKey, upstreamModel string, chatBody []byte, opts cliproxyexecutor.Options) ([]byte, http.Header, error) {
	submitURL := openRouterBatchCollectionURL(baseURL)
	customID := "cliproxy-" + uuid.NewString()
	submitBody, err := buildOpenRouterBatchSubmit(upstreamModel, customID, chatBody)
	if err != nil {
		return nil, nil, err
	}

	submitResp, submitHeaders, err := e.openRouterBatchRequest(ctx, auth, http.MethodPost, submitURL, apiKey, submitBody, opts)
	if err != nil {
		return nil, nil, err
	}
	batchID := strings.TrimSpace(gjson.GetBytes(submitResp, "id").String())
	if batchID == "" {
		return nil, submitHeaders, statusErr{code: http.StatusBadGateway, msg: "openrouter batch submit returned no batch id"}
	}

	deadline := time.Now().Add(openRouterBatchMaxWait)
	pollURL := strings.TrimSuffix(submitURL, "/") + "/" + batchID
	current := submitResp
	for {
		status := strings.ToLower(strings.TrimSpace(gjson.GetBytes(current, "status").String()))
		switch status {
		case "completed":
			completion, errCompletion := openRouterBatchCompletion(current, customID)
			if errCompletion != nil {
				return nil, submitHeaders, errCompletion
			}
			return completion, submitHeaders, nil
		case "failed", "expired", "cancelled", "canceled":
			message := strings.TrimSpace(gjson.GetBytes(current, "error.message").String())
			if message == "" {
				message = strings.TrimSpace(gjson.GetBytes(current, "error").Raw)
			}
			if message == "" || message == "null" {
				message = "openrouter batch " + status
			}
			return nil, submitHeaders, statusErr{code: http.StatusBadGateway, msg: message}
		}
		if time.Now().After(deadline) {
			return nil, submitHeaders, statusErr{code: http.StatusGatewayTimeout, msg: "openrouter batch " + batchID + " did not finish within " + openRouterBatchMaxWait.String()}
		}
		if errWait := waitOpenRouterBatch(ctx, openRouterBatchPollInterval); errWait != nil {
			return nil, submitHeaders, errWait
		}
		polled, _, errPoll := e.openRouterBatchRequest(ctx, auth, http.MethodGet, pollURL, apiKey, nil, opts)
		if errPoll != nil {
			// A batch id can 404 for a while right after POST /batches accepts it.
			// Keep polling until the wait window ends instead of failing the credential.
			if openRouterBatchNotFound(errPoll) {
				continue
			}
			return nil, submitHeaders, errPoll
		}
		current = polled
	}
}

func openRouterBatchNotFound(err error) bool {
	var status statusErr
	if !errors.As(err, &status) || status.code != http.StatusNotFound {
		return false
	}
	message := strings.ToLower(status.msg)
	return strings.Contains(message, "not found")
}

func (e *OpenRouterBatchExecutor) openRouterBatchRequest(ctx context.Context, auth *cliproxyauth.Auth, method, url, apiKey string, body []byte, opts cliproxyexecutor.Options) ([]byte, http.Header, error) {
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, nil, err
	}
	if len(body) > 0 {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("User-Agent", "cli-proxy-openrouter-batch")
	var attrs map[string]string
	if auth != nil {
		attrs = auth.Attributes
	}
	util.ApplyCustomHeadersFromAttrs(httpReq, attrs, opts.Headers)

	var authID, authLabel, authType, authValue string
	if auth != nil {
		authID = auth.ID
		authLabel = auth.Label
		authType, authValue = auth.AccountInfo()
	}
	helps.RecordAPIRequest(ctx, e.cfg, helps.UpstreamRequestLog{
		URL:       url,
		Method:    method,
		Headers:   httpReq.Header.Clone(),
		Body:      body,
		Provider:  e.Identifier(),
		AuthID:    authID,
		AuthLabel: authLabel,
		AuthType:  authType,
		AuthValue: authValue,
	})

	httpClient := helps.NewProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		helps.RecordAPIResponseError(ctx, e.cfg, err)
		return nil, nil, err
	}
	defer func() {
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("openrouter batch executor: close response body error: %v", errClose)
		}
	}()
	helps.RecordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())
	respBody, errRead := io.ReadAll(httpResp.Body)
	if errRead != nil {
		helps.RecordAPIResponseError(ctx, e.cfg, errRead)
		return nil, nil, errRead
	}
	helps.AppendAPIResponseChunk(ctx, e.cfg, respBody)
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		helps.LogWithRequestID(ctx).Debugf("openrouter batch error, status: %d, message: %s", httpResp.StatusCode, helps.SummarizeErrorBody(httpResp.Header.Get("Content-Type"), respBody))
		return nil, httpResp.Header.Clone(), newOpenAICompatStatusError(httpResp.StatusCode, httpResp.Header, respBody)
	}
	return respBody, httpResp.Header.Clone(), nil
}

func buildOpenRouterBatchSubmit(model, customID string, chatBody []byte) ([]byte, error) {
	if !json.Valid(chatBody) {
		return nil, statusErr{code: http.StatusBadRequest, msg: "openrouter batch chat body is not valid json"}
	}
	payload := map[string]any{
		"endpoint": openRouterBatchEndpoint,
		"model":    model,
		"requests": []any{
			map[string]any{
				"custom_id": customID,
				"body":      json.RawMessage(chatBody),
			},
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func openRouterBatchCompletion(batch []byte, customID string) ([]byte, error) {
	results := gjson.GetBytes(batch, "results")
	if !results.Exists() || !results.IsArray() || len(results.Array()) == 0 {
		return nil, statusErr{code: http.StatusBadGateway, msg: "openrouter batch completed without results"}
	}
	var chosen gjson.Result
	found := false
	for _, item := range results.Array() {
		if item.Get("custom_id").String() == customID {
			chosen = item
			found = true
			break
		}
	}
	if !found {
		chosen = results.Array()[0]
	}
	if errNode := chosen.Get("error"); errNode.Exists() && errNode.Type != gjson.Null {
		message := strings.TrimSpace(errNode.Get("message").String())
		if message == "" {
			message = strings.TrimSpace(errNode.Raw)
		}
		return nil, statusErr{code: http.StatusBadGateway, msg: message}
	}
	statusCode := int(chosen.Get("response.status_code").Int())
	responseBody := chosen.Get("response.body")
	if !responseBody.Exists() || responseBody.Type == gjson.Null {
		return nil, statusErr{code: http.StatusBadGateway, msg: "openrouter batch result has no response body"}
	}
	if statusCode != 0 && (statusCode < 200 || statusCode >= 300) {
		return nil, statusErr{code: statusCode, msg: responseBody.Raw}
	}
	return []byte(responseBody.Raw), nil
}

func openRouterBatchCollectionURL(baseURL string) string {
	trimmed := strings.TrimSuffix(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(trimmed, "/batches") {
		return trimmed
	}
	return trimmed + "/batches"
}

func openRouterBatchUpstreamModel(model string) string {
	model = strings.TrimSpace(model)
	if strings.HasSuffix(strings.ToLower(model), ":batch") {
		return model[:len(model)-len(":batch")]
	}
	return model
}

func waitOpenRouterBatch(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
