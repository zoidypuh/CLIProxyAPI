package helps

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

const openRouterMuseModel = "meta/muse-spark-1.3-contributor"
const openRouterMuseRetries = 2

// DoOpenAICompatRequest retries only OpenRouter's intermittent Meta Muse 404.
// Successful responses (including streams) are returned without reading their
// bodies, so an already delivered answer or tool call is never replayed.
func DoOpenAICompatRequest(client *http.Client, req *http.Request, model string, cfg *config.Config, requestLog UpstreamRequestLog) (*http.Response, error) {
	eligible := model == openRouterMuseModel &&
		strings.EqualFold(req.URL.Hostname(), "openrouter.ai") &&
		req.URL.Path == "/api/v1/chat/completions" &&
		req.Method == http.MethodPost && req.GetBody != nil
	current := req
	for attempt := 0; ; attempt++ {
		resp, err := client.Do(current)
		if err != nil || !eligible || resp.StatusCode != http.StatusNotFound || attempt >= openRouterMuseRetries {
			return resp, err
		}
		body, errRead := io.ReadAll(resp.Body)
		if errClose := resp.Body.Close(); errClose != nil {
			LogWithRequestID(req.Context()).Warn("openrouter Muse retry: could not close error response")
		}
		if errRead != nil {
			return nil, errRead
		}
		resp.Body = io.NopCloser(bytes.NewReader(body))
		if !isOpenRouterMuseNotFound(body) {
			return resp, nil
		}

		RecordAPIResponseMetadata(req.Context(), cfg, resp.StatusCode, resp.Header.Clone())
		AppendAPIResponseChunk(req.Context(), cfg, body)
		LogWithRequestID(req.Context()).Warnf("openrouter: Meta returned model_not_found for Muse; retrying upstream request (%d/%d)", attempt+1, openRouterMuseRetries)
		if errWait := waitOpenRouterMuseRetry(req.Context(), time.Duration(attempt+1)*500*time.Millisecond); errWait != nil {
			return nil, errWait
		}
		current = req.Clone(req.Context())
		current.Body, err = req.GetBody()
		if err != nil {
			return nil, err
		}
		RecordAPIRequest(req.Context(), cfg, requestLog)
	}
}

func isOpenRouterMuseNotFound(body []byte) bool {
	var payload struct {
		Error struct {
			Message  string `json:"message"`
			Metadata struct {
				ProviderName      string `json:"provider_name"`
				ProviderErrorCode string `json:"provider_error_code"`
				Raw               string `json:"raw"`
			} `json:"metadata"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.Error.Message != "Provider returned error" || payload.Error.Metadata.ProviderName != "Meta" {
		return false
	}
	if payload.Error.Metadata.ProviderErrorCode != "" {
		return payload.Error.Metadata.ProviderErrorCode == "model_not_found"
	}
	var raw struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	return json.Unmarshal([]byte(payload.Error.Metadata.Raw), &raw) == nil && raw.Error.Code == "model_not_found"
}

func waitOpenRouterMuseRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
