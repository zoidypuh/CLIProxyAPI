package executor

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// metaImageEndpointPath returns the upstream images path when the inbound
// request came from an OpenAI image handler. Chat and responses stay on
// /responses.
func metaImageEndpointPath(opts cliproxyexecutor.Options) string {
	if opts.SourceFormat.String() != metaImageHandlerType {
		return ""
	}
	path := helps.PayloadRequestPath(opts)
	if strings.HasSuffix(path, "/images/edits") {
		return metaImagesEditsPath
	}
	if strings.HasSuffix(path, "/images/generations") {
		return metaImagesGenerationsPath
	}
	return metaDefaultImageEndpoint
}

func (e *MetaExecutor) executeImages(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options, endpointPath string) (resp cliproxyexecutor.Response, err error) {
	enriched, errAuth := e.ensureAuth(ctx, auth)
	if errAuth != nil {
		return resp, errAuth
	}

	model := strings.TrimSpace(gjson.GetBytes(req.Payload, "model").String())
	if model == "" {
		model = strings.TrimSpace(req.Model)
	}
	if slash := strings.LastIndex(model, "/"); slash >= 0 && slash < len(model)-1 {
		model = strings.TrimSpace(model[slash+1:])
	}
	if model == "" {
		model = metaImageModelID
	}

	reporter := helps.NewExecutorUsageReporter(ctx, e, model, enriched)
	defer reporter.TrackFailure(ctx, &err)

	baseURL, token := metaCreds(enriched)
	if strings.TrimSpace(baseURL) == "" {
		return resp, statusErr{code: http.StatusUnauthorized, msg: "meta executor: missing provider baseURL"}
	}
	if endpointPath == "" {
		endpointPath = metaDefaultImageEndpoint
	}

	payload := req.Payload
	if gjson.ValidBytes(payload) {
		payload = helps.SetStringIfDifferent(payload, "model", model)
		payload, _ = sjson.DeleteBytes(payload, "stream")
	}

	url := strings.TrimSuffix(baseURL, "/") + endpointPath
	httpReq, errRequest := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if errRequest != nil {
		return resp, errRequest
	}
	contentType := strings.TrimSpace(opts.Headers.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/json"
	}
	httpReq.Header.Set("Content-Type", contentType)
	applyMetaAPIHeaders(httpReq, enriched, token, false, opts.Headers)
	httpReq.Header.Set("Content-Type", contentType)
	e.recordMetaRequest(ctx, enriched, url, httpReq.Header.Clone(), payload)

	httpClient := helps.NewProxyAwareHTTPClient(ctx, e.cfg, enriched, 0)
	httpClient = reporter.TrackHTTPClient(httpClient)
	httpResp, errDo := httpClient.Do(httpReq)
	if errDo != nil {
		helps.RecordAPIResponseError(ctx, e.cfg, errDo)
		return resp, errDo
	}
	defer func() {
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("meta executor: close response body error: %v", errClose)
		}
	}()
	helps.RecordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())

	data, errRead := io.ReadAll(httpResp.Body)
	if errRead != nil {
		helps.RecordAPIResponseError(ctx, e.cfg, errRead)
		return resp, errRead
	}
	helps.AppendAPIResponseChunk(ctx, e.cfg, data)
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		helps.LogWithRequestID(ctx).Debugf("request error, error status: %d, error message: %s", httpResp.StatusCode, helps.SummarizeErrorBody(httpResp.Header.Get("Content-Type"), data))
		return resp, wrapMetaUpstreamError(httpResp.StatusCode, data)
	}

	reporter.ObserveResponseModel(data)
	reporter.EnsurePublished(ctx)
	return cliproxyexecutor.Response{Payload: data, Headers: httpResp.Header.Clone()}, nil
}
