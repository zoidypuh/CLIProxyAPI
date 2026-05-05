package executor

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
	xxHash64 "github.com/pierrec/xxHash/xxHash64"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func resetClaudeDeviceProfileCache() {
	helps.ResetClaudeDeviceProfileCache()
}

func newClaudeHeaderTestRequest(t *testing.T, incoming http.Header) *http.Request {
	t.Helper()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginReq := httptest.NewRequest(http.MethodPost, "http://localhost/v1/messages", nil)
	ginReq.Header = incoming.Clone()
	ginCtx.Request = ginReq

	req := httptest.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", nil)
	return req.WithContext(context.WithValue(req.Context(), "gin", ginCtx))
}

func newGinContextWithHeaders(t *testing.T, incoming http.Header) context.Context {
	t.Helper()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginReq := httptest.NewRequest(http.MethodPost, "http://localhost/v1/messages", nil)
	ginReq.Header = incoming.Clone()
	ginCtx.Request = ginReq
	return context.WithValue(context.Background(), "gin", ginCtx)
}

func assertClaudeFingerprint(t *testing.T, headers http.Header, userAgent, pkgVersion, runtimeVersion, osName, arch string) {
	t.Helper()

	if got := headers.Get("User-Agent"); got != userAgent {
		t.Fatalf("User-Agent = %q, want %q", got, userAgent)
	}
	if got := headers.Get("X-Stainless-Package-Version"); got != pkgVersion {
		t.Fatalf("X-Stainless-Package-Version = %q, want %q", got, pkgVersion)
	}
	if got := headers.Get("X-Stainless-Runtime-Version"); got != runtimeVersion {
		t.Fatalf("X-Stainless-Runtime-Version = %q, want %q", got, runtimeVersion)
	}
	if got := headers.Get("X-Stainless-Os"); got != osName {
		t.Fatalf("X-Stainless-Os = %q, want %q", got, osName)
	}
	if got := headers.Get("X-Stainless-Arch"); got != arch {
		t.Fatalf("X-Stainless-Arch = %q, want %q", got, arch)
	}
}

func TestApplyClaudeHeaders_UsesConfiguredBaselineFingerprint(t *testing.T) {
	resetClaudeDeviceProfileCache()
	stabilize := true

	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			UserAgent:              "claude-cli/2.1.70 (external, cli)",
			PackageVersion:         "0.80.0",
			RuntimeVersion:         "v24.5.0",
			OS:                     "MacOS",
			Arch:                   "arm64",
			Timeout:                "900",
			StabilizeDeviceProfile: &stabilize,
		},
	}
	auth := &cliproxyauth.Auth{
		ID: "auth-baseline",
		Attributes: map[string]string{
			"api_key":                            "key-baseline",
			"header:User-Agent":                  "evil-client/9.9",
			"header:X-Stainless-Os":              "Linux",
			"header:X-Stainless-Arch":            "x64",
			"header:X-Stainless-Package-Version": "9.9.9",
		},
	}
	incoming := http.Header{
		"User-Agent":                  []string{"curl/8.7.1"},
		"X-Stainless-Package-Version": []string{"0.10.0"},
		"X-Stainless-Runtime-Version": []string{"v18.0.0"},
		"X-Stainless-Os":              []string{"Linux"},
		"X-Stainless-Arch":            []string{"x64"},
	}

	req := newClaudeHeaderTestRequest(t, incoming)
	applyClaudeHeaders(req, auth, "key-baseline", false, nil, cfg)

	assertClaudeFingerprint(t, req.Header, "evil-client/9.9", "9.9.9", "v24.5.0", "Linux", "x64")
	if got := req.Header.Get("X-Stainless-Timeout"); got != "900" {
		t.Fatalf("X-Stainless-Timeout = %q, want %q", got, "900")
	}
}

func TestApplyClaudeHeaders_UsesConfiguredVersionForFallbackUserAgent(t *testing.T) {
	resetClaudeDeviceProfileCache()

	stabilize := false
	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			Version:                "2.2.0",
			PackageVersion:         "0.74.0",
			RuntimeVersion:         "v24.3.0",
			StabilizeDeviceProfile: &stabilize,
		},
	}
	auth := &cliproxyauth.Auth{
		ID: "auth-configured-version-fallback",
		Attributes: map[string]string{
			"api_key": "key-configured-version-fallback",
		},
	}

	req := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent": []string{"curl/8.7.1"},
	})
	applyClaudeHeaders(req, auth, "key-configured-version-fallback", false, nil, cfg)

	assertClaudeFingerprint(t, req.Header, "claude-cli/2.2.0 (external, cli)", "0.74.0", "v24.3.0", helps.MapStainlessOS(), helps.MapStainlessArch())
}

func TestApplyClaudeHeaders_UsesConfiguredUserAgent(t *testing.T) {
	resetClaudeDeviceProfileCache()

	req := httptest.NewRequest(http.MethodPost, "https://example.com/v1/messages", nil)
	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			Version:   "2.3.4",
			UserAgent: "claude-cli/custom-build (external, cli)",
		},
	}

	applyClaudeHeaders(req, nil, "key-123", false, nil, cfg)

	if got := req.Header.Get("User-Agent"); got != "claude-cli/custom-build (external, cli)" {
		t.Fatalf("User-Agent = %q, want custom configured value", got)
	}
}

func TestApplyClaudeHeaders_MatchesCapturedClaudeCliHeaders(t *testing.T) {
	resetClaudeDeviceProfileCache()
	stabilize := true
	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			StabilizeDeviceProfile: &stabilize,
		},
	}
	auth := &cliproxyauth.Auth{
		ID:       "auth-captured-headers",
		Metadata: map[string]any{"access_token": "sk-ant-oat01-test"},
	}

	req := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"OpenAI/Python 2.32.0"},
		"X-Stainless-Lang":            []string{"python"},
		"X-Stainless-Runtime":         []string{"CPython"},
		"X-Stainless-Retry-Count":     []string{"4"},
		"X-Stainless-Runtime-Version": []string{"3.11.14"},
	})
	applyClaudeHeaders(req, auth, "sk-ant-oat01-test", true, nil, cfg)

	if got := req.Header.Get("Authorization"); got != "Bearer sk-ant-oat01-test" {
		t.Fatalf("Authorization = %q, want OAuth bearer token", got)
	}
	if got := req.Header.Get("x-api-key"); got != "" {
		t.Fatalf("x-api-key = %q, want empty for OAuth token", got)
	}
	if got := req.Header.Get("User-Agent"); got != "claude-cli/2.1.124 (external, cli)" {
		t.Fatalf("User-Agent = %q, want captured Claude CLI UA", got)
	}
	if got := req.Header.Get("X-Stainless-Package-Version"); got != "0.81.0" {
		t.Fatalf("X-Stainless-Package-Version = %q, want %q", got, "0.81.0")
	}
	if got := req.Header.Get("X-Stainless-Runtime-Version"); got != "v24.3.0" {
		t.Fatalf("X-Stainless-Runtime-Version = %q, want %q", got, "v24.3.0")
	}
	if got := req.Header.Get("X-Stainless-Os"); got != "Linux" {
		t.Fatalf("X-Stainless-Os = %q, want %q", got, "Linux")
	}
	if got := req.Header.Get("X-Stainless-Arch"); got != "x64" {
		t.Fatalf("X-Stainless-Arch = %q, want %q", got, "x64")
	}
	if got := req.Header.Get("X-Stainless-Lang"); got != "js" {
		t.Fatalf("X-Stainless-Lang = %q, want %q", got, "js")
	}
	if got := req.Header.Get("X-Stainless-Runtime"); got != "node" {
		t.Fatalf("X-Stainless-Runtime = %q, want %q", got, "node")
	}
	if got := req.Header.Get("X-Stainless-Retry-Count"); got != "0" {
		t.Fatalf("X-Stainless-Retry-Count = %q, want %q", got, "0")
	}
	if got := req.Header.Get("Anthropic-Beta"); got != claudeCodeBetaHeader {
		t.Fatalf("Anthropic-Beta = %q, want %q", got, claudeCodeBetaHeader)
	}
	if got := req.Header.Get("Accept"); got != "application/json" {
		t.Fatalf("Accept = %q, want %q", got, "application/json")
	}
	if got := req.Header.Get("Accept-Encoding"); got != "gzip, deflate, br, zstd" {
		t.Fatalf("Accept-Encoding = %q, want captured Claude CLI value", got)
	}
	for _, name := range []string{"x-client-request-id", "Accept-Language", "Sec-Fetch-Mode"} {
		if got := req.Header.Get(name); got != "" {
			t.Fatalf("%s = %q, want empty", name, got)
		}
	}
}

func TestClaudeExecutorPrepareRequest_AnthropicOAuthUsesBearer(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", nil)
	if err != nil {
		t.Fatalf("NewRequest error: %v", err)
	}
	auth := &cliproxyauth.Auth{
		Metadata: map[string]any{"access_token": "sk-ant-oat01-test"},
	}

	if err := NewClaudeExecutor(nil).PrepareRequest(req, auth); err != nil {
		t.Fatalf("PrepareRequest error: %v", err)
	}

	if got := req.Header.Get("Authorization"); got != "Bearer sk-ant-oat01-test" {
		t.Fatalf("Authorization = %q, want OAuth bearer token", got)
	}
	if got := req.Header.Get("x-api-key"); got != "" {
		t.Fatalf("x-api-key = %q, want empty for OAuth token", got)
	}
}

func TestApplyClaudeHeaders_AddsOAuthBetaForClaudeCodeClientOAuth(t *testing.T) {
	resetClaudeDeviceProfileCache()
	stabilize := true
	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			StabilizeDeviceProfile: &stabilize,
		},
	}
	auth := &cliproxyauth.Auth{
		ID:       "auth-oauth-beta",
		Metadata: map[string]any{"access_token": "sk-ant-oat01-test"},
	}
	req := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent": []string{"claude-cli/2.1.126 (external, cli)"},
		"Anthropic-Beta": []string{
			"claude-code-20250219,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,context-management-2025-06-27,prompt-caching-scope-2026-01-05,advisor-tool-2026-03-01,effort-2025-11-24",
		},
	})

	applyClaudeHeaders(req, auth, "sk-ant-oat01-test", true, nil, cfg)

	beta := req.Header.Get("Anthropic-Beta")
	for _, want := range []string{
		"claude-code-20250219",
		"oauth-2025-04-20",
		"advanced-tool-use-2025-11-20",
		"fast-mode-2026-02-01",
	} {
		if !strings.Contains(beta, want) {
			t.Fatalf("Anthropic-Beta missing %q: %q", want, beta)
		}
	}
	if count := strings.Count(beta, "interleaved-thinking-2025-05-14"); count != 1 {
		t.Fatalf("interleaved-thinking beta count = %d, want 1 in %q", count, beta)
	}
}

func TestResolveOutboundClaudeEntrypoint_UsesResolvedDefaultUserAgent(t *testing.T) {
	resetClaudeDeviceProfileCache()
	stabilize := true

	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			StabilizeDeviceProfile: &stabilize,
		},
	}
	auth := &cliproxyauth.Auth{
		ID: "auth-entrypoint-default",
		Attributes: map[string]string{
			"api_key": "key-entrypoint-default",
		},
	}
	ctx := newGinContextWithHeaders(t, http.Header{
		"User-Agent": []string{"curl/8.7.1"},
	})

	if got := resolveOutboundClaudeEntrypoint(ctx, cfg, auth, "key-entrypoint-default"); got != "cli" {
		t.Fatalf("resolveOutboundClaudeEntrypoint() = %q, want %q", got, "cli")
	}
}

func TestResolveOutboundClaudeEntrypoint_AuthHeaderOverrideWins(t *testing.T) {
	resetClaudeDeviceProfileCache()
	stabilize := true

	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			StabilizeDeviceProfile: &stabilize,
		},
	}
	auth := &cliproxyauth.Auth{
		ID: "auth-entrypoint-override",
		Attributes: map[string]string{
			"api_key":           "key-entrypoint-override",
			"header:User-Agent": "claude-cli/2.1.92 (external, vscode)",
		},
	}
	ctx := newGinContextWithHeaders(t, http.Header{
		"User-Agent": []string{"curl/8.7.1"},
	})

	if got := resolveOutboundClaudeEntrypoint(ctx, cfg, auth, "key-entrypoint-override"); got != "vscode" {
		t.Fatalf("resolveOutboundClaudeEntrypoint() = %q, want %q", got, "vscode")
	}
}

func TestApplyClaudeHeaders_TracksHighestClaudeCLIFingerprint(t *testing.T) {
	resetClaudeDeviceProfileCache()
	stabilize := true

	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			UserAgent:              "claude-cli/2.1.60 (external, cli)",
			PackageVersion:         "0.70.0",
			RuntimeVersion:         "v22.0.0",
			OS:                     "MacOS",
			Arch:                   "arm64",
			StabilizeDeviceProfile: &stabilize,
		},
	}
	auth := &cliproxyauth.Auth{
		ID: "auth-upgrade",
		Attributes: map[string]string{
			"api_key": "key-upgrade",
		},
	}

	firstReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"claude-cli/2.1.62 (external, cli)"},
		"X-Stainless-Package-Version": []string{"0.74.0"},
		"X-Stainless-Runtime-Version": []string{"v24.3.0"},
		"X-Stainless-Os":              []string{"Linux"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(firstReq, auth, "key-upgrade", false, nil, cfg)
	assertClaudeFingerprint(t, firstReq.Header, "claude-cli/2.1.62 (external, cli)", "0.74.0", "v24.3.0", "MacOS", "arm64")

	thirdPartyReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"lobe-chat/1.0"},
		"X-Stainless-Package-Version": []string{"0.10.0"},
		"X-Stainless-Runtime-Version": []string{"v18.0.0"},
		"X-Stainless-Os":              []string{"Windows"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(thirdPartyReq, auth, "key-upgrade", false, nil, cfg)
	assertClaudeFingerprint(t, thirdPartyReq.Header, "claude-cli/2.1.62 (external, cli)", "0.74.0", "v24.3.0", "MacOS", "arm64")

	higherReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"claude-cli/2.1.63 (external, cli)"},
		"X-Stainless-Package-Version": []string{"0.75.0"},
		"X-Stainless-Runtime-Version": []string{"v24.4.0"},
		"X-Stainless-Os":              []string{"MacOS"},
		"X-Stainless-Arch":            []string{"arm64"},
	})
	applyClaudeHeaders(higherReq, auth, "key-upgrade", false, nil, cfg)
	assertClaudeFingerprint(t, higherReq.Header, "claude-cli/2.1.63 (external, cli)", "0.75.0", "v24.4.0", "MacOS", "arm64")

	lowerReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"claude-cli/2.1.61 (external, cli)"},
		"X-Stainless-Package-Version": []string{"0.73.0"},
		"X-Stainless-Runtime-Version": []string{"v24.2.0"},
		"X-Stainless-Os":              []string{"Windows"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(lowerReq, auth, "key-upgrade", false, nil, cfg)
	assertClaudeFingerprint(t, lowerReq.Header, "claude-cli/2.1.63 (external, cli)", "0.75.0", "v24.4.0", "MacOS", "arm64")
}

func TestClaudeExecutor_CountTokens_BillingEntrypointMatchesResolvedUserAgent(t *testing.T) {
	resetClaudeDeviceProfileCache()
	stabilize := true
	var seenBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seenBody = bytes.Clone(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"input_tokens":1}`))
	}))
	defer server.Close()

	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			StabilizeDeviceProfile: &stabilize,
		},
	}
	executor := NewClaudeExecutor(cfg)
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "key-123",
		"base_url": server.URL,
	}}
	payload := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)
	ctx := newGinContextWithHeaders(t, http.Header{
		"User-Agent": []string{"curl/8.7.1"},
	})

	_, err := executor.CountTokens(ctx, auth, cliproxyexecutor.Request{
		Model:   "claude-3-5-sonnet-20241022",
		Payload: payload,
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")})
	if err != nil {
		t.Fatalf("CountTokens error: %v", err)
	}

	billingHeader := gjson.GetBytes(seenBody, "system.0.text").String()
	if !strings.Contains(billingHeader, "cc_entrypoint=cli;") {
		t.Fatalf("billing header = %q, want cc_entrypoint=cli", billingHeader)
	}
}

func TestApplyClaudeHeaders_DoesNotDowngradeConfiguredBaselineOnFirstClaudeClient(t *testing.T) {
	resetClaudeDeviceProfileCache()
	stabilize := true

	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			UserAgent:              "claude-cli/2.1.70 (external, cli)",
			PackageVersion:         "0.80.0",
			RuntimeVersion:         "v24.5.0",
			OS:                     "MacOS",
			Arch:                   "arm64",
			StabilizeDeviceProfile: &stabilize,
		},
	}
	auth := &cliproxyauth.Auth{
		ID: "auth-baseline-floor",
		Attributes: map[string]string{
			"api_key": "key-baseline-floor",
		},
	}

	olderClaudeReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"claude-cli/2.1.62 (external, cli)"},
		"X-Stainless-Package-Version": []string{"0.74.0"},
		"X-Stainless-Runtime-Version": []string{"v24.3.0"},
		"X-Stainless-Os":              []string{"Linux"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(olderClaudeReq, auth, "key-baseline-floor", false, nil, cfg)
	assertClaudeFingerprint(t, olderClaudeReq.Header, "claude-cli/2.1.70 (external, cli)", "0.80.0", "v24.5.0", "MacOS", "arm64")

	newerClaudeReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"claude-cli/2.1.71 (external, cli)"},
		"X-Stainless-Package-Version": []string{"0.81.0"},
		"X-Stainless-Runtime-Version": []string{"v24.6.0"},
		"X-Stainless-Os":              []string{"Linux"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(newerClaudeReq, auth, "key-baseline-floor", false, nil, cfg)
	assertClaudeFingerprint(t, newerClaudeReq.Header, "claude-cli/2.1.71 (external, cli)", "0.81.0", "v24.6.0", "MacOS", "arm64")
}

func TestApplyClaudeHeaders_UpgradesCachedSoftwareFingerprintWhenBaselineAdvances(t *testing.T) {
	resetClaudeDeviceProfileCache()
	stabilize := true

	oldCfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			UserAgent:              "claude-cli/2.1.70 (external, cli)",
			PackageVersion:         "0.80.0",
			RuntimeVersion:         "v24.5.0",
			OS:                     "MacOS",
			Arch:                   "arm64",
			StabilizeDeviceProfile: &stabilize,
		},
	}
	newCfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			UserAgent:              "claude-cli/2.1.77 (external, cli)",
			PackageVersion:         "0.87.0",
			RuntimeVersion:         "v24.8.0",
			OS:                     "MacOS",
			Arch:                   "arm64",
			StabilizeDeviceProfile: &stabilize,
		},
	}
	auth := &cliproxyauth.Auth{
		ID: "auth-baseline-reload",
		Attributes: map[string]string{
			"api_key": "key-baseline-reload",
		},
	}

	officialReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"claude-cli/2.1.71 (external, cli)"},
		"X-Stainless-Package-Version": []string{"0.81.0"},
		"X-Stainless-Runtime-Version": []string{"v24.6.0"},
		"X-Stainless-Os":              []string{"Linux"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(officialReq, auth, "key-baseline-reload", false, nil, oldCfg)
	assertClaudeFingerprint(t, officialReq.Header, "claude-cli/2.1.71 (external, cli)", "0.81.0", "v24.6.0", "MacOS", "arm64")

	thirdPartyReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"curl/8.7.1"},
		"X-Stainless-Package-Version": []string{"0.10.0"},
		"X-Stainless-Runtime-Version": []string{"v18.0.0"},
		"X-Stainless-Os":              []string{"Linux"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(thirdPartyReq, auth, "key-baseline-reload", false, nil, newCfg)
	assertClaudeFingerprint(t, thirdPartyReq.Header, "claude-cli/2.1.77 (external, cli)", "0.87.0", "v24.8.0", "MacOS", "arm64")
}

func TestApplyClaudeHeaders_LearnsOfficialFingerprintAfterCustomBaselineFallback(t *testing.T) {
	resetClaudeDeviceProfileCache()
	stabilize := true

	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			UserAgent:              "my-gateway/1.0",
			PackageVersion:         "custom-pkg",
			RuntimeVersion:         "custom-runtime",
			OS:                     "MacOS",
			Arch:                   "arm64",
			StabilizeDeviceProfile: &stabilize,
		},
	}
	auth := &cliproxyauth.Auth{
		ID: "auth-custom-baseline-learning",
		Attributes: map[string]string{
			"api_key": "key-custom-baseline-learning",
		},
	}

	thirdPartyReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"curl/8.7.1"},
		"X-Stainless-Package-Version": []string{"0.10.0"},
		"X-Stainless-Runtime-Version": []string{"v18.0.0"},
		"X-Stainless-Os":              []string{"Linux"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(thirdPartyReq, auth, "key-custom-baseline-learning", false, nil, cfg)
	assertClaudeFingerprint(t, thirdPartyReq.Header, "my-gateway/1.0", "custom-pkg", "custom-runtime", "MacOS", "arm64")

	officialReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"claude-cli/2.1.77 (external, cli)"},
		"X-Stainless-Package-Version": []string{"0.87.0"},
		"X-Stainless-Runtime-Version": []string{"v24.8.0"},
		"X-Stainless-Os":              []string{"Linux"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(officialReq, auth, "key-custom-baseline-learning", false, nil, cfg)
	assertClaudeFingerprint(t, officialReq.Header, "claude-cli/2.1.77 (external, cli)", "0.87.0", "v24.8.0", "MacOS", "arm64")

	postLearningThirdPartyReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"curl/8.7.1"},
		"X-Stainless-Package-Version": []string{"0.10.0"},
		"X-Stainless-Runtime-Version": []string{"v18.0.0"},
		"X-Stainless-Os":              []string{"Linux"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(postLearningThirdPartyReq, auth, "key-custom-baseline-learning", false, nil, cfg)
	assertClaudeFingerprint(t, postLearningThirdPartyReq.Header, "claude-cli/2.1.77 (external, cli)", "0.87.0", "v24.8.0", "MacOS", "arm64")
}

func TestResolveClaudeDeviceProfile_RechecksCacheBeforeStoringCandidate(t *testing.T) {
	resetClaudeDeviceProfileCache()
	stabilize := true

	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			UserAgent:              "claude-cli/2.1.60 (external, cli)",
			PackageVersion:         "0.70.0",
			RuntimeVersion:         "v22.0.0",
			OS:                     "MacOS",
			Arch:                   "arm64",
			StabilizeDeviceProfile: &stabilize,
		},
	}
	auth := &cliproxyauth.Auth{
		ID: "auth-racy-upgrade",
		Attributes: map[string]string{
			"api_key": "key-racy-upgrade",
		},
	}

	lowPaused := make(chan struct{})
	releaseLow := make(chan struct{})
	var pauseOnce sync.Once
	var releaseOnce sync.Once

	helps.ClaudeDeviceProfileBeforeCandidateStore = func(candidate helps.ClaudeDeviceProfile) {
		if candidate.UserAgent != "claude-cli/2.1.62 (external, cli)" {
			return
		}
		pauseOnce.Do(func() { close(lowPaused) })
		<-releaseLow
	}
	t.Cleanup(func() {
		helps.ClaudeDeviceProfileBeforeCandidateStore = nil
		releaseOnce.Do(func() { close(releaseLow) })
	})

	lowResultCh := make(chan helps.ClaudeDeviceProfile, 1)
	go func() {
		lowResultCh <- helps.ResolveClaudeDeviceProfile(auth, "key-racy-upgrade", http.Header{
			"User-Agent":                  []string{"claude-cli/2.1.62 (external, cli)"},
			"X-Stainless-Package-Version": []string{"0.74.0"},
			"X-Stainless-Runtime-Version": []string{"v24.3.0"},
			"X-Stainless-Os":              []string{"Linux"},
			"X-Stainless-Arch":            []string{"x64"},
		}, cfg)
	}()

	select {
	case <-lowPaused:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for lower candidate to pause before storing")
	}

	highResult := helps.ResolveClaudeDeviceProfile(auth, "key-racy-upgrade", http.Header{
		"User-Agent":                  []string{"claude-cli/2.1.63 (external, cli)"},
		"X-Stainless-Package-Version": []string{"0.75.0"},
		"X-Stainless-Runtime-Version": []string{"v24.4.0"},
		"X-Stainless-Os":              []string{"MacOS"},
		"X-Stainless-Arch":            []string{"arm64"},
	}, cfg)
	releaseOnce.Do(func() { close(releaseLow) })

	select {
	case lowResult := <-lowResultCh:
		if lowResult.UserAgent != "claude-cli/2.1.63 (external, cli)" {
			t.Fatalf("lowResult.UserAgent = %q, want %q", lowResult.UserAgent, "claude-cli/2.1.63 (external, cli)")
		}
		if lowResult.PackageVersion != "0.75.0" {
			t.Fatalf("lowResult.PackageVersion = %q, want %q", lowResult.PackageVersion, "0.75.0")
		}
		if lowResult.OS != "MacOS" || lowResult.Arch != "arm64" {
			t.Fatalf("lowResult platform = %s/%s, want %s/%s", lowResult.OS, lowResult.Arch, "MacOS", "arm64")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for lower candidate result")
	}

	if highResult.UserAgent != "claude-cli/2.1.63 (external, cli)" {
		t.Fatalf("highResult.UserAgent = %q, want %q", highResult.UserAgent, "claude-cli/2.1.63 (external, cli)")
	}
	if highResult.OS != "MacOS" || highResult.Arch != "arm64" {
		t.Fatalf("highResult platform = %s/%s, want %s/%s", highResult.OS, highResult.Arch, "MacOS", "arm64")
	}

	cached := helps.ResolveClaudeDeviceProfile(auth, "key-racy-upgrade", http.Header{
		"User-Agent": []string{"curl/8.7.1"},
	}, cfg)
	if cached.UserAgent != "claude-cli/2.1.63 (external, cli)" {
		t.Fatalf("cached.UserAgent = %q, want %q", cached.UserAgent, "claude-cli/2.1.63 (external, cli)")
	}
	if cached.PackageVersion != "0.75.0" {
		t.Fatalf("cached.PackageVersion = %q, want %q", cached.PackageVersion, "0.75.0")
	}
	if cached.OS != "MacOS" || cached.Arch != "arm64" {
		t.Fatalf("cached platform = %s/%s, want %s/%s", cached.OS, cached.Arch, "MacOS", "arm64")
	}
}

func TestApplyClaudeHeaders_ThirdPartyBaselineThenOfficialUpgradeKeepsPinnedPlatform(t *testing.T) {
	resetClaudeDeviceProfileCache()
	stabilize := true

	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			UserAgent:              "claude-cli/2.1.70 (external, cli)",
			PackageVersion:         "0.80.0",
			RuntimeVersion:         "v24.5.0",
			OS:                     "MacOS",
			Arch:                   "arm64",
			StabilizeDeviceProfile: &stabilize,
		},
	}
	auth := &cliproxyauth.Auth{
		ID: "auth-third-party-then-official",
		Attributes: map[string]string{
			"api_key": "key-third-party-then-official",
		},
	}

	thirdPartyReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"curl/8.7.1"},
		"X-Stainless-Package-Version": []string{"0.10.0"},
		"X-Stainless-Runtime-Version": []string{"v18.0.0"},
		"X-Stainless-Os":              []string{"Linux"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(thirdPartyReq, auth, "key-third-party-then-official", false, nil, cfg)
	assertClaudeFingerprint(t, thirdPartyReq.Header, "claude-cli/2.1.70 (external, cli)", "0.80.0", "v24.5.0", "MacOS", "arm64")

	officialReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"claude-cli/2.1.77 (external, cli)"},
		"X-Stainless-Package-Version": []string{"0.87.0"},
		"X-Stainless-Runtime-Version": []string{"v24.8.0"},
		"X-Stainless-Os":              []string{"Linux"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(officialReq, auth, "key-third-party-then-official", false, nil, cfg)
	assertClaudeFingerprint(t, officialReq.Header, "claude-cli/2.1.77 (external, cli)", "0.87.0", "v24.8.0", "MacOS", "arm64")
}

func TestApplyClaudeHeaders_DisableDeviceProfileStabilization(t *testing.T) {
	resetClaudeDeviceProfileCache()

	stabilize := false
	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			UserAgent:              "claude-cli/2.1.60 (external, cli)",
			PackageVersion:         "0.70.0",
			RuntimeVersion:         "v22.0.0",
			OS:                     "MacOS",
			Arch:                   "arm64",
			StabilizeDeviceProfile: &stabilize,
		},
	}
	auth := &cliproxyauth.Auth{
		ID: "auth-disable-stability",
		Attributes: map[string]string{
			"api_key": "key-disable-stability",
		},
	}

	firstReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"claude-cli/2.1.62 (external, cli)"},
		"X-Stainless-Package-Version": []string{"0.74.0"},
		"X-Stainless-Runtime-Version": []string{"v24.3.0"},
		"X-Stainless-Os":              []string{"Linux"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(firstReq, auth, "key-disable-stability", false, nil, cfg)
	assertClaudeFingerprint(t, firstReq.Header, "claude-cli/2.1.62 (external, cli)", "0.74.0", "v24.3.0", "Linux", "x64")

	thirdPartyReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"lobe-chat/1.0"},
		"X-Stainless-Package-Version": []string{"0.10.0"},
		"X-Stainless-Runtime-Version": []string{"v18.0.0"},
		"X-Stainless-Os":              []string{"Windows"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(thirdPartyReq, auth, "key-disable-stability", false, nil, cfg)
	assertClaudeFingerprint(t, thirdPartyReq.Header, "claude-cli/2.1.60 (external, cli)", "0.10.0", "v18.0.0", "Windows", "x64")

	lowerReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"claude-cli/2.1.61 (external, cli)"},
		"X-Stainless-Package-Version": []string{"0.73.0"},
		"X-Stainless-Runtime-Version": []string{"v24.2.0"},
		"X-Stainless-Os":              []string{"Windows"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(lowerReq, auth, "key-disable-stability", false, nil, cfg)
	assertClaudeFingerprint(t, lowerReq.Header, "claude-cli/2.1.61 (external, cli)", "0.73.0", "v24.2.0", "Windows", "x64")
}

func TestApplyClaudeHeaders_LegacyModePreservesConfiguredUserAgentOverrideForClaudeClients(t *testing.T) {
	resetClaudeDeviceProfileCache()

	stabilize := false
	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			UserAgent:              "claude-cli/2.1.60 (external, cli)",
			PackageVersion:         "0.70.0",
			RuntimeVersion:         "v22.0.0",
			StabilizeDeviceProfile: &stabilize,
		},
	}
	auth := &cliproxyauth.Auth{
		ID: "auth-legacy-ua-override",
		Attributes: map[string]string{
			"api_key":           "key-legacy-ua-override",
			"header:User-Agent": "config-ua/1.0",
		},
	}

	req := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"claude-cli/2.1.62 (external, cli)"},
		"X-Stainless-Package-Version": []string{"0.74.0"},
		"X-Stainless-Runtime-Version": []string{"v24.3.0"},
		"X-Stainless-Os":              []string{"Linux"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(req, auth, "key-legacy-ua-override", false, nil, cfg)

	assertClaudeFingerprint(t, req.Header, "config-ua/1.0", "0.74.0", "v24.3.0", "Linux", "x64")
}

func TestApplyClaudeHeaders_LegacyModeFallsBackToRuntimeOSArchWhenMissing(t *testing.T) {
	resetClaudeDeviceProfileCache()

	stabilize := false
	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			UserAgent:              "claude-cli/2.1.60 (external, cli)",
			PackageVersion:         "0.70.0",
			RuntimeVersion:         "v22.0.0",
			OS:                     "MacOS",
			Arch:                   "arm64",
			StabilizeDeviceProfile: &stabilize,
		},
	}
	auth := &cliproxyauth.Auth{
		ID: "auth-legacy-runtime-os-arch",
		Attributes: map[string]string{
			"api_key": "key-legacy-runtime-os-arch",
		},
	}

	req := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent": []string{"curl/8.7.1"},
	})
	applyClaudeHeaders(req, auth, "key-legacy-runtime-os-arch", false, nil, cfg)

	assertClaudeFingerprint(t, req.Header, "claude-cli/2.1.60 (external, cli)", "0.70.0", "v22.0.0", helps.MapStainlessOS(), helps.MapStainlessArch())
}

func TestApplyClaudeHeaders_UnsetStabilizationAlsoUsesLegacyRuntimeOSArchFallback(t *testing.T) {
	resetClaudeDeviceProfileCache()

	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			UserAgent:      "claude-cli/2.1.60 (external, cli)",
			PackageVersion: "0.70.0",
			RuntimeVersion: "v22.0.0",
			OS:             "MacOS",
			Arch:           "arm64",
		},
	}
	auth := &cliproxyauth.Auth{
		ID: "auth-unset-runtime-os-arch",
		Attributes: map[string]string{
			"api_key": "key-unset-runtime-os-arch",
		},
	}

	req := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent": []string{"curl/8.7.1"},
	})
	applyClaudeHeaders(req, auth, "key-unset-runtime-os-arch", false, nil, cfg)

	assertClaudeFingerprint(t, req.Header, "claude-cli/2.1.60 (external, cli)", "0.70.0", "v22.0.0", helps.MapStainlessOS(), helps.MapStainlessArch())
}

func TestClaudeDeviceProfileStabilizationEnabled_DefaultFalse(t *testing.T) {
	if helps.ClaudeDeviceProfileStabilizationEnabled(nil) {
		t.Fatal("expected nil config to default to disabled stabilization")
	}
	if helps.ClaudeDeviceProfileStabilizationEnabled(&config.Config{}) {
		t.Fatal("expected unset stabilize-device-profile to default to disabled stabilization")
	}
}

func TestApplyClaudeToolPrefix(t *testing.T) {
	input := []byte(`{"tools":[{"name":"alpha"},{"name":"proxy_bravo"}],"tool_choice":{"type":"tool","name":"charlie"},"messages":[{"role":"assistant","content":[{"type":"tool_use","name":"delta","id":"t1","input":{}}]}]}`)
	out := applyClaudeToolPrefix(input, "proxy_")

	if got := gjson.GetBytes(out, "tools.0.name").String(); got != "proxy_alpha" {
		t.Fatalf("tools.0.name = %q, want %q", got, "proxy_alpha")
	}
	if got := gjson.GetBytes(out, "tools.1.name").String(); got != "proxy_bravo" {
		t.Fatalf("tools.1.name = %q, want %q", got, "proxy_bravo")
	}
	if got := gjson.GetBytes(out, "tool_choice.name").String(); got != "proxy_charlie" {
		t.Fatalf("tool_choice.name = %q, want %q", got, "proxy_charlie")
	}
	if got := gjson.GetBytes(out, "messages.0.content.0.name").String(); got != "proxy_delta" {
		t.Fatalf("messages.0.content.0.name = %q, want %q", got, "proxy_delta")
	}
}

func TestApplyClaudeToolPrefix_WithToolReference(t *testing.T) {
	input := []byte(`{"tools":[{"name":"alpha"}],"messages":[{"role":"user","content":[{"type":"tool_reference","tool_name":"beta"},{"type":"tool_reference","tool_name":"proxy_gamma"}]}]}`)
	out := applyClaudeToolPrefix(input, "proxy_")

	if got := gjson.GetBytes(out, "messages.0.content.0.tool_name").String(); got != "proxy_beta" {
		t.Fatalf("messages.0.content.0.tool_name = %q, want %q", got, "proxy_beta")
	}
	if got := gjson.GetBytes(out, "messages.0.content.1.tool_name").String(); got != "proxy_gamma" {
		t.Fatalf("messages.0.content.1.tool_name = %q, want %q", got, "proxy_gamma")
	}
}

func TestApplyClaudeToolPrefix_SkipsBuiltinTools(t *testing.T) {
	input := []byte(`{"tools":[{"type":"web_search_20250305","name":"web_search"},{"name":"my_custom_tool","input_schema":{"type":"object"}}]}`)
	out := applyClaudeToolPrefix(input, "proxy_")

	if got := gjson.GetBytes(out, "tools.0.name").String(); got != "web_search" {
		t.Fatalf("built-in tool name should not be prefixed: tools.0.name = %q, want %q", got, "web_search")
	}
	if got := gjson.GetBytes(out, "tools.1.name").String(); got != "proxy_my_custom_tool" {
		t.Fatalf("custom tool should be prefixed: tools.1.name = %q, want %q", got, "proxy_my_custom_tool")
	}
}

func TestApplyClaudeToolPrefix_BuiltinToolSkipped(t *testing.T) {
	body := []byte(`{
		"tools": [
			{"type": "web_search_20250305", "name": "web_search", "max_uses": 5},
			{"name": "Read"}
		],
		"messages": [
			{"role": "user", "content": [
				{"type": "tool_use", "name": "web_search", "id": "ws1", "input": {}},
				{"type": "tool_use", "name": "Read", "id": "r1", "input": {}}
			]}
		]
	}`)
	out := applyClaudeToolPrefix(body, "proxy_")

	if got := gjson.GetBytes(out, "tools.0.name").String(); got != "web_search" {
		t.Fatalf("tools.0.name = %q, want %q", got, "web_search")
	}
	if got := gjson.GetBytes(out, "messages.0.content.0.name").String(); got != "web_search" {
		t.Fatalf("messages.0.content.0.name = %q, want %q", got, "web_search")
	}
	if got := gjson.GetBytes(out, "tools.1.name").String(); got != "proxy_Read" {
		t.Fatalf("tools.1.name = %q, want %q", got, "proxy_Read")
	}
	if got := gjson.GetBytes(out, "messages.0.content.1.name").String(); got != "proxy_Read" {
		t.Fatalf("messages.0.content.1.name = %q, want %q", got, "proxy_Read")
	}
}

func TestApplyClaudeToolPrefix_KnownBuiltinInHistoryOnly(t *testing.T) {
	body := []byte(`{
		"tools": [
			{"name": "Read"}
		],
		"messages": [
			{"role": "user", "content": [
				{"type": "tool_use", "name": "web_search", "id": "ws1", "input": {}}
			]}
		]
	}`)
	out := applyClaudeToolPrefix(body, "proxy_")

	if got := gjson.GetBytes(out, "messages.0.content.0.name").String(); got != "web_search" {
		t.Fatalf("messages.0.content.0.name = %q, want %q", got, "web_search")
	}
	if got := gjson.GetBytes(out, "tools.0.name").String(); got != "proxy_Read" {
		t.Fatalf("tools.0.name = %q, want %q", got, "proxy_Read")
	}
}

func TestApplyClaudeToolPrefix_CustomToolsPrefixed(t *testing.T) {
	body := []byte(`{
		"tools": [{"name": "Read"}, {"name": "Write"}],
		"messages": [
			{"role": "user", "content": [
				{"type": "tool_use", "name": "Read", "id": "r1", "input": {}},
				{"type": "tool_use", "name": "Write", "id": "w1", "input": {}}
			]}
		]
	}`)
	out := applyClaudeToolPrefix(body, "proxy_")

	if got := gjson.GetBytes(out, "tools.0.name").String(); got != "proxy_Read" {
		t.Fatalf("tools.0.name = %q, want %q", got, "proxy_Read")
	}
	if got := gjson.GetBytes(out, "tools.1.name").String(); got != "proxy_Write" {
		t.Fatalf("tools.1.name = %q, want %q", got, "proxy_Write")
	}
	if got := gjson.GetBytes(out, "messages.0.content.0.name").String(); got != "proxy_Read" {
		t.Fatalf("messages.0.content.0.name = %q, want %q", got, "proxy_Read")
	}
	if got := gjson.GetBytes(out, "messages.0.content.1.name").String(); got != "proxy_Write" {
		t.Fatalf("messages.0.content.1.name = %q, want %q", got, "proxy_Write")
	}
}

func TestApplyClaudeToolPrefix_ToolChoiceBuiltin(t *testing.T) {
	body := []byte(`{
		"tools": [
			{"type": "web_search_20250305", "name": "web_search"},
			{"name": "Read"}
		],
		"tool_choice": {"type": "tool", "name": "web_search"}
	}`)
	out := applyClaudeToolPrefix(body, "proxy_")

	if got := gjson.GetBytes(out, "tool_choice.name").String(); got != "web_search" {
		t.Fatalf("tool_choice.name = %q, want %q", got, "web_search")
	}
}

func TestApplyClaudeToolPrefix_KnownFallbackBuiltinsRemainUnprefixed(t *testing.T) {
	for _, builtin := range []string{"web_search", "code_execution", "text_editor", "computer"} {
		t.Run(builtin, func(t *testing.T) {
			input := []byte(fmt.Sprintf(`{
				"tools":[{"name":"Read"}],
				"tool_choice":{"type":"tool","name":%q},
				"messages":[{"role":"assistant","content":[{"type":"tool_use","name":%q,"id":"toolu_1","input":{}},{"type":"tool_reference","tool_name":%q},{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"tool_reference","tool_name":%q}]}]}]
			}`, builtin, builtin, builtin, builtin))
			out := applyClaudeToolPrefix(input, "proxy_")

			if got := gjson.GetBytes(out, "tool_choice.name").String(); got != builtin {
				t.Fatalf("tool_choice.name = %q, want %q", got, builtin)
			}
			if got := gjson.GetBytes(out, "messages.0.content.0.name").String(); got != builtin {
				t.Fatalf("messages.0.content.0.name = %q, want %q", got, builtin)
			}
			if got := gjson.GetBytes(out, "messages.0.content.1.tool_name").String(); got != builtin {
				t.Fatalf("messages.0.content.1.tool_name = %q, want %q", got, builtin)
			}
			if got := gjson.GetBytes(out, "messages.0.content.2.content.0.tool_name").String(); got != builtin {
				t.Fatalf("messages.0.content.2.content.0.tool_name = %q, want %q", got, builtin)
			}
			if got := gjson.GetBytes(out, "tools.0.name").String(); got != "proxy_Read" {
				t.Fatalf("tools.0.name = %q, want %q", got, "proxy_Read")
			}
		})
	}
}

func TestStripClaudeToolPrefixFromResponse(t *testing.T) {
	input := []byte(`{"content":[{"type":"tool_use","name":"proxy_alpha","id":"t1","input":{}},{"type":"tool_use","name":"bravo","id":"t2","input":{}}]}`)
	out := stripClaudeToolPrefixFromResponse(input, "proxy_")

	if got := gjson.GetBytes(out, "content.0.name").String(); got != "alpha" {
		t.Fatalf("content.0.name = %q, want %q", got, "alpha")
	}
	if got := gjson.GetBytes(out, "content.1.name").String(); got != "bravo" {
		t.Fatalf("content.1.name = %q, want %q", got, "bravo")
	}
}

func TestStripClaudeToolPrefixFromResponse_WithToolReference(t *testing.T) {
	input := []byte(`{"content":[{"type":"tool_reference","tool_name":"proxy_alpha"},{"type":"tool_reference","tool_name":"bravo"}]}`)
	out := stripClaudeToolPrefixFromResponse(input, "proxy_")

	if got := gjson.GetBytes(out, "content.0.tool_name").String(); got != "alpha" {
		t.Fatalf("content.0.tool_name = %q, want %q", got, "alpha")
	}
	if got := gjson.GetBytes(out, "content.1.tool_name").String(); got != "bravo" {
		t.Fatalf("content.1.tool_name = %q, want %q", got, "bravo")
	}
}

func TestStripClaudeToolPrefixFromStreamLine(t *testing.T) {
	line := []byte(`data: {"type":"content_block_start","content_block":{"type":"tool_use","name":"proxy_alpha","id":"t1"},"index":0}`)
	out := stripClaudeToolPrefixFromStreamLine(line, "proxy_")

	payload := bytes.TrimSpace(out)
	if bytes.HasPrefix(payload, []byte("data:")) {
		payload = bytes.TrimSpace(payload[len("data:"):])
	}
	if got := gjson.GetBytes(payload, "content_block.name").String(); got != "alpha" {
		t.Fatalf("content_block.name = %q, want %q", got, "alpha")
	}
}

func TestStripClaudeToolPrefixFromStreamLine_WithToolReference(t *testing.T) {
	line := []byte(`data: {"type":"content_block_start","content_block":{"type":"tool_reference","tool_name":"proxy_beta"},"index":0}`)
	out := stripClaudeToolPrefixFromStreamLine(line, "proxy_")

	payload := bytes.TrimSpace(out)
	if bytes.HasPrefix(payload, []byte("data:")) {
		payload = bytes.TrimSpace(payload[len("data:"):])
	}
	if got := gjson.GetBytes(payload, "content_block.tool_name").String(); got != "beta" {
		t.Fatalf("content_block.tool_name = %q, want %q", got, "beta")
	}
}

func TestApplyClaudeToolPrefix_NestedToolReference(t *testing.T) {
	input := []byte(`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_123","content":[{"type":"tool_reference","tool_name":"mcp__nia__manage_resource"}]}]}]}`)
	out := applyClaudeToolPrefix(input, "proxy_")
	got := gjson.GetBytes(out, "messages.0.content.0.content.0.tool_name").String()
	if got != "proxy_mcp__nia__manage_resource" {
		t.Fatalf("nested tool_reference tool_name = %q, want %q", got, "proxy_mcp__nia__manage_resource")
	}
}

func TestClaudeExecutor_ReusesUserIDAcrossModelsWhenCacheEnabled(t *testing.T) {
	var userIDs []string
	var requestModels []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		userID := gjson.GetBytes(body, "metadata.user_id").String()
		model := gjson.GetBytes(body, "model").String()
		userIDs = append(userIDs, userID)
		requestModels = append(requestModels, model)
		t.Logf("HTTP Server received request: model=%s, user_id=%s, url=%s", model, userID, r.URL.String())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","model":"claude-3-5-sonnet","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	t.Logf("End-to-end test: Fake HTTP server started at %s", server.URL)

	cacheEnabled := true
	executor := NewClaudeExecutor(&config.Config{
		ClaudeKey: []config.ClaudeKey{
			{
				APIKey:  "key-123",
				BaseURL: server.URL,
				Cloak: &config.CloakConfig{
					CacheUserID: &cacheEnabled,
				},
			},
		},
	})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "key-123",
		"base_url": server.URL,
	}}

	payload := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)
	models := []string{"claude-3-5-sonnet", "claude-3-5-haiku"}
	for _, model := range models {
		t.Logf("Sending request for model: %s", model)
		modelPayload, _ := sjson.SetBytes(payload, "model", model)
		if _, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
			Model:   model,
			Payload: modelPayload,
		}, cliproxyexecutor.Options{
			SourceFormat: sdktranslator.FromString("claude"),
		}); err != nil {
			t.Fatalf("Execute(%s) error: %v", model, err)
		}
	}

	if len(userIDs) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(userIDs))
	}
	if userIDs[0] == "" || userIDs[1] == "" {
		t.Fatal("expected user_id to be populated")
	}
	t.Logf("user_id[0] (model=%s): %s", requestModels[0], userIDs[0])
	t.Logf("user_id[1] (model=%s): %s", requestModels[1], userIDs[1])
	if userIDs[0] != userIDs[1] {
		t.Fatalf("expected user_id to be reused across models, got %q and %q", userIDs[0], userIDs[1])
	}
	if !helps.IsValidUserID(userIDs[0]) {
		t.Fatalf("user_id %q is not valid", userIDs[0])
	}
	t.Logf("✓ End-to-end test passed: Same user_id (%s) was used for both models", userIDs[0])
}

func TestClaudeExecutor_GeneratesNewUserIDByDefault(t *testing.T) {
	var userIDs []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		userIDs = append(userIDs, gjson.GetBytes(body, "metadata.user_id").String())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","model":"claude-3-5-sonnet","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "key-123",
		"base_url": server.URL,
	}}

	payload := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	for i := 0; i < 2; i++ {
		if _, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
			Model:   "claude-3-5-sonnet",
			Payload: payload,
		}, cliproxyexecutor.Options{
			SourceFormat: sdktranslator.FromString("claude"),
		}); err != nil {
			t.Fatalf("Execute call %d error: %v", i, err)
		}
	}

	if len(userIDs) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(userIDs))
	}
	if userIDs[0] == "" || userIDs[1] == "" {
		t.Fatal("expected user_id to be populated")
	}
	if userIDs[0] == userIDs[1] {
		t.Fatalf("expected user_id to change when caching is not enabled, got identical values %q", userIDs[0])
	}
	if !helps.IsValidUserID(userIDs[0]) || !helps.IsValidUserID(userIDs[1]) {
		t.Fatalf("user_ids should be valid, got %q and %q", userIDs[0], userIDs[1])
	}
}

func TestStripClaudeToolPrefixFromResponse_NestedToolReference(t *testing.T) {
	input := []byte(`{"content":[{"type":"tool_result","tool_use_id":"toolu_123","content":[{"type":"tool_reference","tool_name":"proxy_mcp__nia__manage_resource"}]}]}`)
	out := stripClaudeToolPrefixFromResponse(input, "proxy_")
	got := gjson.GetBytes(out, "content.0.content.0.tool_name").String()
	if got != "mcp__nia__manage_resource" {
		t.Fatalf("nested tool_reference tool_name = %q, want %q", got, "mcp__nia__manage_resource")
	}
}

func TestApplyClaudeToolPrefix_NestedToolReferenceWithStringContent(t *testing.T) {
	// tool_result.content can be a string - should not be processed
	input := []byte(`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_123","content":"plain string result"}]}]}`)
	out := applyClaudeToolPrefix(input, "proxy_")
	got := gjson.GetBytes(out, "messages.0.content.0.content").String()
	if got != "plain string result" {
		t.Fatalf("string content should remain unchanged = %q", got)
	}
}

func TestApplyClaudeToolPrefix_SkipsBuiltinToolReference(t *testing.T) {
	input := []byte(`{"tools":[{"type":"web_search_20250305","name":"web_search"}],"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"tool_reference","tool_name":"web_search"}]}]}]}`)
	out := applyClaudeToolPrefix(input, "proxy_")
	got := gjson.GetBytes(out, "messages.0.content.0.content.0.tool_name").String()
	if got != "web_search" {
		t.Fatalf("built-in tool_reference should not be prefixed, got %q", got)
	}
}

func TestNormalizeCacheControlTTL_DowngradesLaterOneHourBlocks(t *testing.T) {
	payload := []byte(`{
		"tools": [{"name":"t1","cache_control":{"type":"ephemeral","ttl":"1h"}}],
		"system": [{"type":"text","text":"s1","cache_control":{"type":"ephemeral"}}],
		"messages": [{"role":"user","content":[{"type":"text","text":"u1","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]
	}`)

	out := normalizeCacheControlTTL(payload)

	if got := gjson.GetBytes(out, "tools.0.cache_control.ttl").String(); got != "1h" {
		t.Fatalf("tools.0.cache_control.ttl = %q, want %q", got, "1h")
	}
	if gjson.GetBytes(out, "messages.0.content.0.cache_control.ttl").Exists() {
		t.Fatalf("messages.0.content.0.cache_control.ttl should be removed after a default-5m block")
	}
}

func TestNormalizeCacheControlTTL_PreservesOriginalBytesWhenNoChange(t *testing.T) {
	// Payload where no TTL normalization is needed (all blocks use 1h with no
	// preceding 5m block). The text intentionally contains HTML chars (<, >, &)
	// that json.Marshal would escape to \u003c etc., altering byte identity.
	payload := []byte(`{"tools":[{"name":"t1","cache_control":{"type":"ephemeral","ttl":"1h"}}],"system":[{"type":"text","text":"<system-reminder>foo & bar</system-reminder>","cache_control":{"type":"ephemeral","ttl":"1h"}}],"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)

	out := normalizeCacheControlTTL(payload)

	if !bytes.Equal(out, payload) {
		t.Fatalf("normalizeCacheControlTTL altered bytes when no change was needed.\noriginal: %s\ngot:      %s", payload, out)
	}
}

func TestNormalizeCacheControlTTL_PreservesKeyOrderWhenModified(t *testing.T) {
	payload := []byte(`{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"u1","cache_control":{"type":"ephemeral","ttl":"1h"}}]}],"tools":[{"name":"t1","cache_control":{"type":"ephemeral"}}],"system":[{"type":"text","text":"s1","cache_control":{"type":"ephemeral"}}]}`)

	out := normalizeCacheControlTTL(payload)

	if gjson.GetBytes(out, "messages.0.content.0.cache_control.ttl").Exists() {
		t.Fatalf("messages.0.content.0.cache_control.ttl should be removed after a default-5m block")
	}

	outStr := string(out)
	idxModel := strings.Index(outStr, `"model"`)
	idxMessages := strings.Index(outStr, `"messages"`)
	idxTools := strings.Index(outStr, `"tools"`)
	idxSystem := strings.Index(outStr, `"system"`)
	if idxModel == -1 || idxMessages == -1 || idxTools == -1 || idxSystem == -1 {
		t.Fatalf("failed to locate top-level keys in output: %s", outStr)
	}
	if !(idxModel < idxMessages && idxMessages < idxTools && idxTools < idxSystem) {
		t.Fatalf("top-level key order changed:\noriginal: %s\ngot:      %s", payload, out)
	}
}

func TestEnforceCacheControlLimit_StripsNonLastToolBeforeMessages(t *testing.T) {
	payload := []byte(`{
		"tools": [
			{"name":"t1","cache_control":{"type":"ephemeral"}},
			{"name":"t2","cache_control":{"type":"ephemeral"}}
		],
		"system": [{"type":"text","text":"s1","cache_control":{"type":"ephemeral"}}],
		"messages": [
			{"role":"user","content":[{"type":"text","text":"u1","cache_control":{"type":"ephemeral"}}]},
			{"role":"user","content":[{"type":"text","text":"u2","cache_control":{"type":"ephemeral"}}]}
		]
	}`)

	out := enforceCacheControlLimit(payload, 4)

	if got := countCacheControls(out); got != 4 {
		t.Fatalf("cache_control count = %d, want 4", got)
	}
	if gjson.GetBytes(out, "tools.0.cache_control").Exists() {
		t.Fatalf("tools.0.cache_control should be removed first (non-last tool)")
	}
	if !gjson.GetBytes(out, "tools.1.cache_control").Exists() {
		t.Fatalf("tools.1.cache_control (last tool) should be preserved")
	}
	if !gjson.GetBytes(out, "messages.0.content.0.cache_control").Exists() || !gjson.GetBytes(out, "messages.1.content.0.cache_control").Exists() {
		t.Fatalf("message cache_control blocks should be preserved when non-last tool removal is enough")
	}
}

func TestEnforceCacheControlLimit_PreservesKeyOrderWhenModified(t *testing.T) {
	payload := []byte(`{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"u1","cache_control":{"type":"ephemeral"}},{"type":"text","text":"u2","cache_control":{"type":"ephemeral"}}]}],"tools":[{"name":"t1","cache_control":{"type":"ephemeral"}},{"name":"t2","cache_control":{"type":"ephemeral"}}],"system":[{"type":"text","text":"s1","cache_control":{"type":"ephemeral"}}]}`)

	out := enforceCacheControlLimit(payload, 4)

	if got := countCacheControls(out); got != 4 {
		t.Fatalf("cache_control count = %d, want 4", got)
	}
	if gjson.GetBytes(out, "tools.0.cache_control").Exists() {
		t.Fatalf("tools.0.cache_control should be removed first (non-last tool)")
	}

	outStr := string(out)
	idxModel := strings.Index(outStr, `"model"`)
	idxMessages := strings.Index(outStr, `"messages"`)
	idxTools := strings.Index(outStr, `"tools"`)
	idxSystem := strings.Index(outStr, `"system"`)
	if idxModel == -1 || idxMessages == -1 || idxTools == -1 || idxSystem == -1 {
		t.Fatalf("failed to locate top-level keys in output: %s", outStr)
	}
	if !(idxModel < idxMessages && idxMessages < idxTools && idxTools < idxSystem) {
		t.Fatalf("top-level key order changed:\noriginal: %s\ngot:      %s", payload, out)
	}
}

func TestEnforceCacheControlLimit_ToolOnlyPayloadStillRespectsLimit(t *testing.T) {
	payload := []byte(`{
		"tools": [
			{"name":"t1","cache_control":{"type":"ephemeral"}},
			{"name":"t2","cache_control":{"type":"ephemeral"}},
			{"name":"t3","cache_control":{"type":"ephemeral"}},
			{"name":"t4","cache_control":{"type":"ephemeral"}},
			{"name":"t5","cache_control":{"type":"ephemeral"}}
		]
	}`)

	out := enforceCacheControlLimit(payload, 4)

	if got := countCacheControls(out); got != 4 {
		t.Fatalf("cache_control count = %d, want 4", got)
	}
	if gjson.GetBytes(out, "tools.0.cache_control").Exists() {
		t.Fatalf("tools.0.cache_control should be removed to satisfy max=4")
	}
	if !gjson.GetBytes(out, "tools.4.cache_control").Exists() {
		t.Fatalf("last tool cache_control should be preserved when possible")
	}
}

func TestClaudeExecutor_CountTokens_AppliesCacheControlGuards(t *testing.T) {
	var seenBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seenBody = bytes.Clone(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"input_tokens":42}`))
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "key-123",
		"base_url": server.URL,
	}}

	payload := []byte(`{
		"tools": [
			{"name":"t1","cache_control":{"type":"ephemeral","ttl":"1h"}},
			{"name":"t2","cache_control":{"type":"ephemeral"}}
		],
		"system": [
			{"type":"text","text":"s1","cache_control":{"type":"ephemeral","ttl":"1h"}},
			{"type":"text","text":"s2","cache_control":{"type":"ephemeral","ttl":"1h"}}
		],
		"messages": [
			{"role":"user","content":[{"type":"text","text":"u1","cache_control":{"type":"ephemeral","ttl":"1h"}}]},
			{"role":"user","content":[{"type":"text","text":"u2","cache_control":{"type":"ephemeral","ttl":"1h"}}]}
		]
	}`)

	_, err := executor.CountTokens(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-3-5-haiku-20241022",
		Payload: payload,
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")})
	if err != nil {
		t.Fatalf("CountTokens error: %v", err)
	}

	if len(seenBody) == 0 {
		t.Fatal("expected count_tokens request body to be captured")
	}
	if got := countCacheControls(seenBody); got > 4 {
		t.Fatalf("count_tokens body has %d cache_control blocks, want <= 4", got)
	}
	if hasTTLOrderingViolation(seenBody) {
		t.Fatalf("count_tokens body still has ttl ordering violations: %s", string(seenBody))
	}
}

func TestClaudeExecutor_CountTokens_UsesConfiguredClaudeVersion(t *testing.T) {
	var seenBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seenBody = bytes.Clone(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"input_tokens":42}`))
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			Version: "2.2.0",
		},
	})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "key-123",
		"base_url": server.URL,
	}}
	payload := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	_, err := executor.CountTokens(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-3-5-sonnet-20241022",
		Payload: payload,
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")})
	if err != nil {
		t.Fatalf("CountTokens error: %v", err)
	}

	if len(seenBody) == 0 {
		t.Fatal("expected count_tokens request body to be captured")
	}
	if got := gjson.GetBytes(seenBody, "system.0.text").String(); !strings.Contains(got, "cc_version=2.2.0.") {
		t.Fatalf("count_tokens billing header should use configured Claude version, got %q", got)
	}
}

func TestClaudeExecutor_CountTokens_EscapesConfiguredClaudeVersionInBillingHeader(t *testing.T) {
	var seenBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seenBody = bytes.Clone(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"input_tokens":42}`))
	}))
	defer server.Close()

	configuredVersion := `2.2.0\"beta\\build`
	executor := NewClaudeExecutor(&config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			Version: configuredVersion,
		},
	})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "key-123",
		"base_url": server.URL,
	}}
	payload := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	_, err := executor.CountTokens(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-3-5-sonnet-20241022",
		Payload: payload,
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")})
	if err != nil {
		t.Fatalf("CountTokens error: %v", err)
	}

	if len(seenBody) == 0 {
		t.Fatal("expected count_tokens request body to be captured")
	}
	if !gjson.ValidBytes(seenBody) {
		t.Fatalf("count_tokens request body should remain valid JSON: %s", string(seenBody))
	}
	got := gjson.GetBytes(seenBody, "system.0.text").String()
	if !strings.Contains(got, "cc_version="+configuredVersion+".") {
		t.Fatalf("count_tokens billing header should preserve configured Claude version, got %q", got)
	}
}

func hasTTLOrderingViolation(payload []byte) bool {
	seen5m := false
	violates := false

	checkCC := func(cc gjson.Result) {
		if !cc.Exists() || violates {
			return
		}
		ttl := cc.Get("ttl").String()
		if ttl != "1h" {
			seen5m = true
			return
		}
		if seen5m {
			violates = true
		}
	}

	tools := gjson.GetBytes(payload, "tools")
	if tools.IsArray() {
		tools.ForEach(func(_, tool gjson.Result) bool {
			checkCC(tool.Get("cache_control"))
			return !violates
		})
	}

	system := gjson.GetBytes(payload, "system")
	if system.IsArray() {
		system.ForEach(func(_, item gjson.Result) bool {
			checkCC(item.Get("cache_control"))
			return !violates
		})
	}

	messages := gjson.GetBytes(payload, "messages")
	if messages.IsArray() {
		messages.ForEach(func(_, msg gjson.Result) bool {
			content := msg.Get("content")
			if content.IsArray() {
				content.ForEach(func(_, item gjson.Result) bool {
					checkCC(item.Get("cache_control"))
					return !violates
				})
			}
			return !violates
		})
	}

	return violates
}

func TestClaudeExecutor_Execute_InvalidGzipErrorBodyReturnsDecodeMessage(t *testing.T) {
	testClaudeExecutorInvalidCompressedErrorBody(t, func(executor *ClaudeExecutor, auth *cliproxyauth.Auth, payload []byte) error {
		_, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
			Model:   "claude-3-5-sonnet-20241022",
			Payload: payload,
		}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")})
		return err
	})
}

func TestClaudeExecutor_ExecuteStream_InvalidGzipErrorBodyReturnsDecodeMessage(t *testing.T) {
	testClaudeExecutorInvalidCompressedErrorBody(t, func(executor *ClaudeExecutor, auth *cliproxyauth.Auth, payload []byte) error {
		_, err := executor.ExecuteStream(context.Background(), auth, cliproxyexecutor.Request{
			Model:   "claude-3-5-sonnet-20241022",
			Payload: payload,
		}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")})
		return err
	})
}

func TestClaudeExecutor_CountTokens_InvalidGzipErrorBodyReturnsDecodeMessage(t *testing.T) {
	testClaudeExecutorInvalidCompressedErrorBody(t, func(executor *ClaudeExecutor, auth *cliproxyauth.Auth, payload []byte) error {
		_, err := executor.CountTokens(context.Background(), auth, cliproxyexecutor.Request{
			Model:   "claude-3-5-sonnet-20241022",
			Payload: payload,
		}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")})
		return err
	})
}

func testClaudeExecutorInvalidCompressedErrorBody(
	t *testing.T,
	invoke func(executor *ClaudeExecutor, auth *cliproxyauth.Auth, payload []byte) error,
) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("not-a-valid-gzip-stream"))
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "key-123",
		"base_url": server.URL,
	}}
	payload := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	err := invoke(executor, auth, payload)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to decode error response body") {
		t.Fatalf("expected decode failure message, got: %v", err)
	}
	if statusProvider, ok := err.(interface{ StatusCode() int }); !ok || statusProvider.StatusCode() != http.StatusBadRequest {
		t.Fatalf("expected status code 400, got: %v", err)
	}
}

func TestEnsureModelMaxTokens_UsesRegisteredMaxCompletionTokens(t *testing.T) {
	reg := registry.GetGlobalRegistry()
	clientID := "test-claude-max-completion-tokens-client"
	modelID := "test-claude-max-completion-tokens-model"
	reg.RegisterClient(clientID, "claude", []*registry.ModelInfo{{
		ID:                  modelID,
		Type:                "claude",
		OwnedBy:             "anthropic",
		Object:              "model",
		Created:             time.Now().Unix(),
		MaxCompletionTokens: 4096,
		UserDefined:         true,
	}})
	defer reg.UnregisterClient(clientID)

	input := []byte(`{"model":"test-claude-max-completion-tokens-model","messages":[{"role":"user","content":"hi"}]}`)
	out := ensureModelMaxTokens(input, modelID)

	if got := gjson.GetBytes(out, "max_tokens").Int(); got != 4096 {
		t.Fatalf("max_tokens = %d, want %d", got, 4096)
	}
}

func TestEnsureModelMaxTokens_DefaultsMissingValue(t *testing.T) {
	reg := registry.GetGlobalRegistry()
	clientID := "test-claude-default-max-tokens-client"
	modelID := "test-claude-default-max-tokens-model"
	reg.RegisterClient(clientID, "claude", []*registry.ModelInfo{{
		ID:          modelID,
		Type:        "claude",
		OwnedBy:     "anthropic",
		Object:      "model",
		Created:     time.Now().Unix(),
		UserDefined: true,
	}})
	defer reg.UnregisterClient(clientID)

	input := []byte(`{"model":"test-claude-default-max-tokens-model","messages":[{"role":"user","content":"hi"}]}`)
	out := ensureModelMaxTokens(input, modelID)

	if got := gjson.GetBytes(out, "max_tokens").Int(); got != defaultModelMaxTokens {
		t.Fatalf("max_tokens = %d, want %d", got, defaultModelMaxTokens)
	}
}

func TestEnsureModelMaxTokens_PreservesExplicitValue(t *testing.T) {
	reg := registry.GetGlobalRegistry()
	clientID := "test-claude-preserve-max-tokens-client"
	modelID := "test-claude-preserve-max-tokens-model"
	reg.RegisterClient(clientID, "claude", []*registry.ModelInfo{{
		ID:                  modelID,
		Type:                "claude",
		OwnedBy:             "anthropic",
		Object:              "model",
		Created:             time.Now().Unix(),
		MaxCompletionTokens: 4096,
		UserDefined:         true,
	}})
	defer reg.UnregisterClient(clientID)

	input := []byte(`{"model":"test-claude-preserve-max-tokens-model","max_tokens":2048,"messages":[{"role":"user","content":"hi"}]}`)
	out := ensureModelMaxTokens(input, modelID)

	if got := gjson.GetBytes(out, "max_tokens").Int(); got != 2048 {
		t.Fatalf("max_tokens = %d, want %d", got, 2048)
	}
}

func TestEnsureModelMaxTokens_SkipsUnregisteredModel(t *testing.T) {
	input := []byte(`{"model":"test-claude-unregistered-model","messages":[{"role":"user","content":"hi"}]}`)
	out := ensureModelMaxTokens(input, "test-claude-unregistered-model")

	if gjson.GetBytes(out, "max_tokens").Exists() {
		t.Fatalf("max_tokens should remain unset, got %s", gjson.GetBytes(out, "max_tokens").Raw)
	}
}

// TestClaudeExecutor_ExecuteStream_SetsClaudeCliAcceptHeaders verifies that streaming
// requests use the same Accept headers as current Claude Code.
func TestClaudeExecutor_ExecuteStream_SetsClaudeCliAcceptHeaders(t *testing.T) {
	var gotEncoding, gotAccept string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEncoding = r.Header.Get("Accept-Encoding")
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "key-123",
		"base_url": server.URL,
	}}
	payload := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	result, err := executor.ExecuteStream(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-3-5-sonnet-20241022",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
	})
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("unexpected chunk error: %v", chunk.Err)
		}
	}

	if gotEncoding != "gzip, deflate, br, zstd" {
		t.Errorf("Accept-Encoding = %q, want %q", gotEncoding, "gzip, deflate, br, zstd")
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept = %q, want %q", gotAccept, "application/json")
	}
}

// TestClaudeExecutor_Execute_SetsCompressedAcceptEncoding verifies that non-streaming
// requests keep the full accept-encoding to allow response compression (which
// decodeResponseBody handles correctly).
func TestClaudeExecutor_Execute_SetsCompressedAcceptEncoding(t *testing.T) {
	var gotEncoding, gotAccept string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEncoding = r.Header.Get("Accept-Encoding")
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","model":"claude-3-5-sonnet-20241022","role":"assistant","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "key-123",
		"base_url": server.URL,
	}}
	payload := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	_, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-3-5-sonnet-20241022",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if gotEncoding != "gzip, deflate, br, zstd" {
		t.Errorf("Accept-Encoding = %q, want %q", gotEncoding, "gzip, deflate, br, zstd")
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept = %q, want %q", gotAccept, "application/json")
	}
}

// TestClaudeExecutor_ExecuteStream_GzipSuccessBodyDecoded verifies that a streaming
// HTTP 200 response with Content-Encoding: gzip is correctly decompressed before
// the line scanner runs, so SSE chunks are not silently dropped.
func TestClaudeExecutor_ExecuteStream_GzipSuccessBodyDecoded(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write([]byte("data: {\"type\":\"message_stop\"}\n"))
	_ = gz.Close()
	compressedBody := buf.Bytes()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(compressedBody)
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "key-123",
		"base_url": server.URL,
	}}
	payload := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	result, err := executor.ExecuteStream(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-3-5-sonnet-20241022",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
	})
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}

	var combined strings.Builder
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("chunk error: %v", chunk.Err)
		}
		combined.Write(chunk.Payload)
	}

	if combined.Len() == 0 {
		t.Fatal("expected at least one chunk from gzip-encoded SSE body, got none (body was not decompressed)")
	}
	if !strings.Contains(combined.String(), "message_stop") {
		t.Errorf("expected SSE content in chunks, got: %q", combined.String())
	}
}

// TestDecodeResponseBody_MagicByteGzipNoHeader verifies that decodeResponseBody
// detects gzip-compressed content via magic bytes even when Content-Encoding is absent.
func TestDecodeResponseBody_MagicByteGzipNoHeader(t *testing.T) {
	const plaintext = "data: {\"type\":\"message_stop\"}\n"

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write([]byte(plaintext))
	_ = gz.Close()

	rc := io.NopCloser(&buf)
	decoded, err := decodeResponseBody(rc, "")
	if err != nil {
		t.Fatalf("decodeResponseBody error: %v", err)
	}
	defer decoded.Close()

	got, err := io.ReadAll(decoded)
	if err != nil {
		t.Fatalf("ReadAll error: %v", err)
	}
	if string(got) != plaintext {
		t.Errorf("decoded = %q, want %q", got, plaintext)
	}
}

// TestDecodeResponseBody_MagicByteZstdNoHeader verifies that decodeResponseBody
// detects zstd-compressed content via magic bytes even when Content-Encoding is absent.
func TestDecodeResponseBody_MagicByteZstdNoHeader(t *testing.T) {
	const plaintext = "data: {\"type\":\"message_stop\"}\n"

	var buf bytes.Buffer
	enc, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatalf("zstd.NewWriter: %v", err)
	}
	_, _ = enc.Write([]byte(plaintext))
	_ = enc.Close()

	rc := io.NopCloser(&buf)
	decoded, err := decodeResponseBody(rc, "")
	if err != nil {
		t.Fatalf("decodeResponseBody error: %v", err)
	}
	defer decoded.Close()

	got, err := io.ReadAll(decoded)
	if err != nil {
		t.Fatalf("ReadAll error: %v", err)
	}
	if string(got) != plaintext {
		t.Errorf("decoded = %q, want %q", got, plaintext)
	}
}

// TestDecodeResponseBody_PlainTextNoHeader verifies that decodeResponseBody returns
// plain text untouched when Content-Encoding is absent and no magic bytes match.
func TestDecodeResponseBody_PlainTextNoHeader(t *testing.T) {
	const plaintext = "data: {\"type\":\"message_stop\"}\n"
	rc := io.NopCloser(strings.NewReader(plaintext))
	decoded, err := decodeResponseBody(rc, "")
	if err != nil {
		t.Fatalf("decodeResponseBody error: %v", err)
	}
	defer decoded.Close()

	got, err := io.ReadAll(decoded)
	if err != nil {
		t.Fatalf("ReadAll error: %v", err)
	}
	if string(got) != plaintext {
		t.Errorf("decoded = %q, want %q", got, plaintext)
	}
}

// TestClaudeExecutor_ExecuteStream_GzipNoContentEncodingHeader verifies the full
// pipeline: when the upstream returns a gzip-compressed SSE body WITHOUT setting
// Content-Encoding (a misbehaving upstream), the magic-byte sniff in
// decodeResponseBody still decompresses it, so chunks reach the caller.
func TestClaudeExecutor_ExecuteStream_GzipNoContentEncodingHeader(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write([]byte("data: {\"type\":\"message_stop\"}\n"))
	_ = gz.Close()
	compressedBody := buf.Bytes()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// Intentionally omit Content-Encoding to simulate misbehaving upstream.
		_, _ = w.Write(compressedBody)
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "key-123",
		"base_url": server.URL,
	}}
	payload := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	result, err := executor.ExecuteStream(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-3-5-sonnet-20241022",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
	})
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}

	var combined strings.Builder
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("chunk error: %v", chunk.Err)
		}
		combined.Write(chunk.Payload)
	}

	if combined.Len() == 0 {
		t.Fatal("expected chunks from gzip body without Content-Encoding header, got none (magic-byte sniff failed)")
	}
	if !strings.Contains(combined.String(), "message_stop") {
		t.Errorf("unexpected chunk content: %q", combined.String())
	}
}

// TestClaudeExecutor_Execute_GzipErrorBodyNoContentEncodingHeader verifies that the
// error path (4xx) correctly decompresses a gzip body even when the upstream omits
// the Content-Encoding header.  This closes the gap left by PR #1771, which only
// fixed header-declared compression on the error path.
func TestClaudeExecutor_Execute_GzipErrorBodyNoContentEncodingHeader(t *testing.T) {
	const errJSON = `{"type":"error","error":{"type":"invalid_request_error","message":"test error"}}`

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write([]byte(errJSON))
	_ = gz.Close()
	compressedBody := buf.Bytes()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Intentionally omit Content-Encoding to simulate misbehaving upstream.
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write(compressedBody)
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "key-123",
		"base_url": server.URL,
	}}
	payload := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	_, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-3-5-sonnet-20241022",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
	})
	if err == nil {
		t.Fatal("expected an error for 400 response, got nil")
	}
	if !strings.Contains(err.Error(), "test error") {
		t.Errorf("error message should contain decompressed JSON, got: %q", err.Error())
	}
}

// TestClaudeExecutor_ExecuteStream_GzipErrorBodyNoContentEncodingHeader verifies
// the same for the streaming executor: 4xx gzip body without Content-Encoding is
// decoded and the error message is readable.
func TestClaudeExecutor_ExecuteStream_GzipErrorBodyNoContentEncodingHeader(t *testing.T) {
	const errJSON = `{"type":"error","error":{"type":"invalid_request_error","message":"stream test error"}}`

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write([]byte(errJSON))
	_ = gz.Close()
	compressedBody := buf.Bytes()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Intentionally omit Content-Encoding to simulate misbehaving upstream.
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write(compressedBody)
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "key-123",
		"base_url": server.URL,
	}}
	payload := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	_, err := executor.ExecuteStream(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-3-5-sonnet-20241022",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
	})
	if err == nil {
		t.Fatal("expected an error for 400 response, got nil")
	}
	if !strings.Contains(err.Error(), "stream test error") {
		t.Errorf("error message should contain decompressed JSON, got: %q", err.Error())
	}
}

// TestClaudeExecutor_ExecuteStream_AcceptEncodingOverrideCannotBypassClaudeCliDefault verifies that
// the streaming executor keeps the current Claude Code Accept-Encoding regardless of auth.Attributes override.
func TestClaudeExecutor_ExecuteStream_AcceptEncodingOverrideCannotBypassClaudeCliDefault(t *testing.T) {
	var gotEncoding string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEncoding = r.Header.Get("Accept-Encoding")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":                "key-123",
		"base_url":               server.URL,
		"header:Accept-Encoding": "identity",
	}}
	payload := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	result, err := executor.ExecuteStream(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-3-5-sonnet-20241022",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
	})
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("unexpected chunk error: %v", chunk.Err)
		}
	}

	if gotEncoding != "gzip, deflate, br, zstd" {
		t.Errorf("Accept-Encoding = %q; stream path must enforce current Claude Code default regardless of auth.Attributes override", gotEncoding)
	}
}

func expectedClaudeCodeCombinedPrompt() string {
	return strings.Join([]string{
		helps.ClaudeCodeIntro,
		helps.ClaudeCodeSystem,
		helps.ClaudeCodeDoingTasks,
		helps.ClaudeCodeToneAndStyle,
		helps.ClaudeCodeOutputEfficiency,
	}, "\n\n")
}

func expectedForwardedSystemReminder(text string) string {
	return fmt.Sprintf(`<system-reminder>
As you answer the user's questions, you can use the following context from the system:
%s

IMPORTANT: this context may or may not be relevant to your tasks. You should not respond to this context unless it is highly relevant to your task.
</system-reminder>
`, text)
}

func assertPrependedReminderBlock(t *testing.T, out []byte, reminderText, userText string) {
	t.Helper()

	if got := gjson.GetBytes(out, "messages.0.content.#").Int(); got != 2 {
		t.Fatalf("messages.0.content length = %d, want 2", got)
	}
	if got := gjson.GetBytes(out, "messages.0.content.0.cache_control.type").String(); got != "ephemeral" {
		t.Fatalf("messages.0.content.0.cache_control.type = %q, want ephemeral", got)
	}
	if got := gjson.GetBytes(out, "messages.0.content.0.text").String(); got != expectedForwardedSystemReminder(reminderText) {
		t.Fatalf("messages.0.content.0.text = %q, want forwarded reminder", got)
	}
	if got := gjson.GetBytes(out, "messages.0.content.1.text").String(); got != userText {
		t.Fatalf("messages.0.content.1.text = %q, want %q", got, userText)
	}
}

// Test case 1: String system prompt is preserved by forwarding it to the first user message
func TestCheckSystemInstructionsWithMode_StringSystemPreserved(t *testing.T) {
	payload := []byte(`{"system":"You are a helpful assistant.","messages":[{"role":"user","content":"hi"}]}`)

	out := checkSystemInstructionsWithMode(payload, false, nil)

	system := gjson.GetBytes(out, "system")
	if !system.IsArray() {
		t.Fatalf("system should be an array, got %s", system.Type)
	}

	blocks := system.Array()
	if len(blocks) != 3 {
		t.Fatalf("expected 3 system blocks (billing+agent+combined prompt), got %d", len(blocks))
	}

	if !strings.HasPrefix(blocks[0].Get("text").String(), "x-anthropic-billing-header:") {
		t.Fatalf("blocks[0] should be billing header, got %q", blocks[0].Get("text").String())
	}
	if blocks[1].Get("text").String() != "You are Claude Code, Anthropic's official CLI for Claude." {
		t.Fatalf("blocks[1] should be agent block, got %q", blocks[1].Get("text").String())
	}
	if blocks[1].Get("cache_control.type").String() != "ephemeral" {
		t.Fatalf("blocks[1] should have ephemeral cache_control, got %s", blocks[1].Get("cache_control").Raw)
	}
	if !blocks[2].Get("cache_control").Exists() {
		t.Fatalf("blocks[2] should have cache_control, got %s", blocks[2].Get("cache_control").Raw)
	}
	if blocks[2].Get("text").String() != expectedClaudeCodeCombinedPrompt() {
		t.Fatalf("blocks[2] should be combined Claude Code prompt, got %q", blocks[2].Get("text").String())
	}
	assertPrependedReminderBlock(t, out, "You are a helpful assistant.", "hi")
}

// Test case 2: Strict mode keeps the injected Claude Code system blocks only
func TestCheckSystemInstructionsWithMode_StringSystemStrict(t *testing.T) {
	payload := []byte(`{"system":"You are a helpful assistant.","messages":[{"role":"user","content":"hi"}]}`)

	out := checkSystemInstructionsWithMode(payload, true, nil)

	blocks := gjson.GetBytes(out, "system").Array()
	if len(blocks) != 3 {
		t.Fatalf("strict mode should produce 3 system blocks, got %d", len(blocks))
	}
	if got := gjson.GetBytes(out, "messages.0.content").String(); got != "hi" {
		t.Fatalf("strict mode should not forward system prompt into messages, got %q", got)
	}
}

// Test case 3: Empty string system prompt does not alter the first user message
func TestCheckSystemInstructionsWithMode_EmptyStringSystemIgnored(t *testing.T) {
	payload := []byte(`{"system":"","messages":[{"role":"user","content":"hi"}]}`)

	out := checkSystemInstructionsWithMode(payload, false, nil)

	blocks := gjson.GetBytes(out, "system").Array()
	if len(blocks) != 3 {
		t.Fatalf("empty string system should produce 3 blocks, got %d", len(blocks))
	}
	if got := gjson.GetBytes(out, "messages.0.content").String(); got != "hi" {
		t.Fatalf("empty string system should not alter messages, got %q", got)
	}
}

// Test case 4: Array system prompt is forwarded to the first user message
func TestCheckSystemInstructionsWithMode_ArraySystemStillWorks(t *testing.T) {
	payload := []byte(`{"system":[{"type":"text","text":"Be concise."}],"messages":[{"role":"user","content":"hi"}]}`)

	out := checkSystemInstructionsWithMode(payload, false, nil)

	blocks := gjson.GetBytes(out, "system").Array()
	if len(blocks) != 3 {
		t.Fatalf("expected 3 system blocks, got %d", len(blocks))
	}
	assertPrependedReminderBlock(t, out, "Be concise.", "hi")
}

// Test case 5: Special characters in string system prompt survive forwarding
func TestCheckSystemInstructionsWithMode_StringWithSpecialChars(t *testing.T) {
	payload := []byte(`{"system":"Use <xml> tags & \"quotes\" in output.","messages":[{"role":"user","content":"hi"}]}`)

	out := checkSystemInstructionsWithMode(payload, false, nil)

	blocks := gjson.GetBytes(out, "system").Array()
	if len(blocks) != 3 {
		t.Fatalf("expected 3 system blocks, got %d", len(blocks))
	}
	assertPrependedReminderBlock(t, out, `Use <xml> tags & "quotes" in output.`, "hi")
}

func TestCheckSystemInstructionsWithMode_UsesConfiguredClaudeVersion(t *testing.T) {
	payload := []byte(`{"system":"You are a helpful assistant.","messages":[{"role":"user","content":"hi"}]}`)
	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			Version: "2.2.0",
		},
	}

	out := checkSystemInstructionsWithMode(payload, false, cfg)

	billingHeader := gjson.GetBytes(out, "system.0.text").String()
	if !strings.Contains(billingHeader, "cc_version=2.2.0.") {
		t.Fatalf("billing header should use configured Claude version, got %q", billingHeader)
	}
}

func TestCheckSystemInstructionsWithMode_EscapesConfiguredClaudeVersion(t *testing.T) {
	configuredVersion := `2.2.0\"beta\\build`
	payload := []byte(`{"system":"You are a helpful assistant.","messages":[{"role":"user","content":"hi"}]}`)
	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			Version: configuredVersion,
		},
	}

	out := checkSystemInstructionsWithMode(payload, false, cfg)

	if !gjson.ValidBytes(out) {
		t.Fatalf("payload should remain valid JSON: %s", string(out))
	}
	system := gjson.GetBytes(out, "system").Array()
	if len(system) != 3 {
		t.Fatalf("expected 3 system blocks, got %d", len(system))
	}
	billingHeader := system[0].Get("text").String()
	if !strings.Contains(billingHeader, "cc_version="+configuredVersion+".") {
		t.Fatalf("billing header should preserve configured Claude version, got %q", billingHeader)
	}
	if got := system[1].Get("text").String(); got != "You are Claude Code, Anthropic's official CLI for Claude." {
		t.Fatalf("system[1] should remain the agent block, got %q", got)
	}
	if got := system[2].Get("text").String(); got != expectedClaudeCodeCombinedPrompt() {
		t.Fatalf("system[2] should remain the combined Claude Code prompt, got %q", got)
	}
	assertPrependedReminderBlock(t, out, "You are a helpful assistant.", "hi")
}

func TestCheckSystemInstructionsWithSigningMode_OAuthPreservesShortSystemAsSystemBlock(t *testing.T) {
	payload := []byte(`{"system":"# Mara\nIf asked for the proxy marker, reply exactly: MARA_PROXY_MARKER_9f7b2c","messages":[{"role":"user","content":"proxy marker?"}]}`)

	out := checkSystemInstructionsWithSigningMode(payload, false, true, true, "2.1.119", "cli", "")

	blocks := gjson.GetBytes(out, "system").Array()
	if len(blocks) != 4 {
		t.Fatalf("OAuth mode should preserve short system as fourth block, got %d blocks: %s", len(blocks), string(out))
	}
	if got := blocks[3].Get("text").String(); !strings.Contains(got, "MARA_PROXY_MARKER_9f7b2c") {
		t.Fatalf("system[3] should preserve forwarded persona/system text, got %q", got)
	}
	if got := gjson.GetBytes(out, "messages.0.content").String(); got != "proxy marker?" {
		t.Fatalf("OAuth mode should not move system text into user message, got %q", got)
	}
}

func TestCheckSystemInstructionsWithSigningMode_OAuthStrictStillPreservesShortSystem(t *testing.T) {
	payload := []byte(`{"system":"# Sisyphus\nIf asked who you are, answer as Sisyphus.","messages":[{"role":"user","content":"who are you?"}]}`)

	out := checkSystemInstructionsWithSigningMode(payload, true, true, true, "2.1.119", "cli", "")

	blocks := gjson.GetBytes(out, "system").Array()
	if len(blocks) != 4 {
		t.Fatalf("OAuth strict mode should preserve short system as fourth block, got %d blocks: %s", len(blocks), string(out))
	}
	if got := blocks[3].Get("text").String(); !strings.Contains(got, "Sisyphus") {
		t.Fatalf("system[3] should preserve forwarded short system text, got %q", got)
	}
	if got := gjson.GetBytes(out, "messages.0.content").String(); got != "who are you?" {
		t.Fatalf("OAuth strict mode should not move system text into user message, got %q", got)
	}
}

func TestRestoreOriginalSystemMessages_RestoresOpenAISystemRoleForClaudeCloak(t *testing.T) {
	translated := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"text","text":"proxy marker?"}]}]}`)
	original := []byte(`{"messages":[{"role":"system","content":"# Mara\nIf asked for the proxy marker, reply exactly: MARA_PROXY_MARKER_9f7b2c"},{"role":"user","content":"proxy marker?"}]}`)

	restored := restoreOriginalSystemMessages(translated, original)
	out := checkSystemInstructionsWithSigningMode(restored, false, true, true, "2.1.119", "cli", "")

	if got := gjson.GetBytes(out, "system.3.text").String(); !strings.Contains(got, "MARA_PROXY_MARKER_9f7b2c") {
		t.Fatalf("system[3] should include restored OpenAI system role, got %q; body=%s", got, string(out))
	}
	if got := gjson.GetBytes(out, "messages.0.content.0.text").String(); got != "proxy marker?" {
		t.Fatalf("user message should stay intact, got %q", got)
	}
}

func TestRestoreOriginalSystemMessages_DoesNotDuplicateNativeClaudeSystemArray(t *testing.T) {
	translated := []byte(`{"model":"claude-haiku-4-5-20251001","system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.126.b83; cc_entrypoint=cli; cch=9ce6b;"},{"type":"text","text":"You are Claude Code, Anthropic's official CLI for Claude."},{"type":"text","text":"Generate a concise title."}],"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)
	original := []byte(`{"model":"claude-haiku-4-5-20251001","system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.126.b83; cc_entrypoint=cli; cch=9ce6b;"},{"type":"text","text":"You are Claude Code, Anthropic's official CLI for Claude."},{"type":"text","text":"Generate a concise title."}],"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	out := restoreOriginalSystemMessages(translated, original)

	blocks := gjson.GetBytes(out, "system").Array()
	if len(blocks) != 3 {
		t.Fatalf("system blocks should not be duplicated, got %d blocks: %s", len(blocks), string(out))
	}
	if got := blocks[2].Get("text").String(); got != "Generate a concise title." {
		t.Fatalf("system[2].text = %q, want title prompt", got)
	}
}

func TestSanitizeForwardedSystemPrompt_OnlyParaphrasesLargeTemplates(t *testing.T) {
	short := "# Mara\nIf asked for the proxy marker, reply exactly: MARA_PROXY_MARKER_9f7b2c"
	if got := sanitizeForwardedSystemPrompt(short); got != short {
		t.Fatalf("short persona system should be preserved, got %q", got)
	}

	largeTemplate := strings.Repeat("x", 12000)
	if got := sanitizeForwardedSystemPrompt(largeTemplate); got == largeTemplate || !strings.Contains(got, "Use the available tools") {
		t.Fatalf("large template should be paraphrased, got %q", got)
	}
}

func TestSanitizeForwardedSystemPrompt_PreservesSoulFromLargeTemplates(t *testing.T) {
	largeSoulTemplate := strings.Join([]string{
		"# CLAUDE",
		"SOUL.md - Core Personality",
		"If asked who you are, answer as Claude with the dry persona.",
		"",
		"You have persistent memory across sessions. This should be trimmed.",
		"<available_skills>",
		strings.Repeat("x", 12000),
	}, "\n")

	got := sanitizeForwardedSystemPrompt(largeSoulTemplate)

	if !strings.Contains(got, "SOUL.md - Core Personality") {
		t.Fatalf("SOUL block should be preserved, got %q", got)
	}
	if !strings.Contains(got, "answer as Claude with the dry persona") {
		t.Fatalf("persona instructions should be preserved, got %q", got)
	}
	if strings.Contains(got, "You have persistent memory") || strings.Contains(got, "<available_skills>") {
		t.Fatalf("large non-SOUL scaffolding should still be trimmed, got %q", got)
	}
	if !strings.Contains(got, "Use the available tools when needed") {
		t.Fatalf("neutral tool reminder should still be appended, got %q", got)
	}
}

func TestClaudeExecutor_ExperimentalCCHSigningDisabledByDefaultKeepsLegacyHeader(t *testing.T) {
	var seenBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seenBody = bytes.Clone(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","model":"claude-3-5-sonnet","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "key-123",
		"base_url": server.URL,
	}}
	payload := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	_, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-3-5-sonnet-20241022",
		Payload: payload,
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(seenBody) == 0 {
		t.Fatal("expected request body to be captured")
	}

	billingHeader := gjson.GetBytes(seenBody, "system.0.text").String()
	if !strings.HasPrefix(billingHeader, "x-anthropic-billing-header:") {
		t.Fatalf("system.0.text = %q, want billing header", billingHeader)
	}
	if strings.Contains(billingHeader, "cch=00000;") {
		t.Fatalf("legacy mode should not forward cch placeholder, got %q", billingHeader)
	}
}

func TestClaudeExecutor_ExperimentalCCHSigningOptInSignsFinalBody(t *testing.T) {
	var seenBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seenBody = bytes.Clone(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","model":"claude-3-5-sonnet","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{
		ClaudeKey: []config.ClaudeKey{{
			APIKey:                 "key-123",
			BaseURL:                server.URL,
			ExperimentalCCHSigning: true,
		}},
	})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "key-123",
		"base_url": server.URL,
	}}
	const messageText = "please keep literal cch=00000 in this message"
	payload := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"please keep literal cch=00000 in this message"}]}]}`)

	_, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-3-5-sonnet-20241022",
		Payload: payload,
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(seenBody) == 0 {
		t.Fatal("expected request body to be captured")
	}
	if got := gjson.GetBytes(seenBody, "messages.0.content.0.text").String(); got != messageText {
		t.Fatalf("message text = %q, want %q", got, messageText)
	}

	billingPattern := regexp.MustCompile(`(x-anthropic-billing-header:[^"]*?\bcch=)([0-9a-f]{5})(;)`)
	match := billingPattern.FindSubmatch(seenBody)
	if match == nil {
		t.Fatalf("expected signed billing header in body: %s", string(seenBody))
	}
	actualCCH := string(match[2])
	unsignedBody := billingPattern.ReplaceAll(seenBody, []byte(`${1}00000${3}`))
	wantCCH := fmt.Sprintf("%05x", xxHash64.Checksum(unsignedBody, 0x6E52736AC806831E)&0xFFFFF)
	if actualCCH != wantCCH {
		t.Fatalf("cch = %q, want %q\nbody: %s", actualCCH, wantCCH, string(seenBody))
	}
}

func TestApplyCloaking_PreservesConfiguredStrictModeAndSensitiveWordsWhenModeOmitted(t *testing.T) {
	cfg := &config.Config{
		ClaudeKey: []config.ClaudeKey{{
			APIKey: "key-123",
			Cloak: &config.CloakConfig{
				StrictMode:     true,
				SensitiveWords: []string{"proxy"},
			},
		}},
	}
	auth := &cliproxyauth.Auth{Attributes: map[string]string{"api_key": "key-123"}}
	payload := []byte(`{"system":"proxy rules","messages":[{"role":"user","content":[{"type":"text","text":"proxy access"}]}]}`)

	out := applyCloaking(context.Background(), cfg, auth, payload, "claude-3-5-sonnet-20241022", "key-123")

	blocks := gjson.GetBytes(out, "system").Array()
	if len(blocks) != 3 {
		t.Fatalf("expected 3 injected system blocks (billing+agent+combined prompt), got %d", len(blocks))
	}
	if got := gjson.GetBytes(out, "messages.0.content.#").Int(); got != 1 {
		t.Fatalf("strict mode should not prepend a forwarded system reminder block, got %d content blocks", got)
	}
	if got := gjson.GetBytes(out, "messages.0.content.0.text").String(); !strings.Contains(got, "\u200B") {
		t.Fatalf("expected configured sensitive word obfuscation to apply, got %q", got)
	}
}

func TestApplyClaudeCodeOAuthRequestDefaults_AddsCapturedClaudeCodeFields(t *testing.T) {
	payload := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)
	out := applyClaudeCodeOAuthRequestDefaults(payload)

	if got := gjson.GetBytes(out, "thinking.type").String(); got != "adaptive" {
		t.Fatalf("thinking.type = %q, want %q", got, "adaptive")
	}
	if got := gjson.GetBytes(out, "output_config.effort").String(); got != "medium" {
		t.Fatalf("output_config.effort = %q, want %q", got, "medium")
	}
	if got := gjson.GetBytes(out, "context_management.edits.0.type").String(); got != "clear_thinking_20251015" {
		t.Fatalf("context_management.edits.0.type = %q, want clear_thinking_20251015", got)
	}
	if got := gjson.GetBytes(out, "context_management.edits.0.keep").String(); got != "all" {
		t.Fatalf("context_management.edits.0.keep = %q, want all", got)
	}
}

func TestApplyClaudeCodeOAuthRequestDefaults_PreservesManualThinking(t *testing.T) {
	payload := []byte(`{"thinking":{"type":"enabled","budget_tokens":2048},"output_config":{"effort":"max"},"context_management":{"edits":[{"type":"custom"}]}}`)
	out := applyClaudeCodeOAuthRequestDefaults(payload)

	if got := gjson.GetBytes(out, "thinking.type").String(); got != "enabled" {
		t.Fatalf("thinking.type = %q, want enabled", got)
	}
	if got := gjson.GetBytes(out, "thinking.budget_tokens").Int(); got != 2048 {
		t.Fatalf("thinking.budget_tokens = %d, want 2048", got)
	}
	if got := gjson.GetBytes(out, "output_config.effort").String(); got != "max" {
		t.Fatalf("output_config.effort = %q, want max", got)
	}
	if got := gjson.GetBytes(out, "context_management.edits.0.type").String(); got != "custom" {
		t.Fatalf("context_management.edits.0.type = %q, want custom", got)
	}
}

func TestSupportsClaudeCodeOAuthDefaults_DisablesHaikuDefaults(t *testing.T) {
	for _, model := range []string{
		"claude-haiku-4-5-20251001",
		"claude-3-5-haiku-20241022",
	} {
		if supportsClaudeCodeOAuthDefaults(model) {
			t.Fatalf("supportsClaudeCodeOAuthDefaults(%q) = true, want false", model)
		}
	}

	if !supportsClaudeCodeOAuthDefaults("claude-sonnet-4-6") {
		t.Fatal("sonnet should keep Claude Code OAuth defaults")
	}
}

func TestNormalizeClaudeTemperatureForThinking_AdaptiveCoercesToOne(t *testing.T) {
	payload := []byte(`{"temperature":0,"thinking":{"type":"adaptive"},"output_config":{"effort":"max"}}`)
	out := normalizeClaudeTemperatureForThinking(payload)

	if got := gjson.GetBytes(out, "temperature").Float(); got != 1 {
		t.Fatalf("temperature = %v, want 1", got)
	}
}

func TestNormalizeClaudeTemperatureForThinking_EnabledCoercesToOne(t *testing.T) {
	payload := []byte(`{"temperature":0.2,"thinking":{"type":"enabled","budget_tokens":2048}}`)
	out := normalizeClaudeTemperatureForThinking(payload)

	if got := gjson.GetBytes(out, "temperature").Float(); got != 1 {
		t.Fatalf("temperature = %v, want 1", got)
	}
}

func TestNormalizeClaudeTemperatureForThinking_NoThinkingLeavesTemperatureAlone(t *testing.T) {
	payload := []byte(`{"temperature":0,"messages":[{"role":"user","content":"hi"}]}`)
	out := normalizeClaudeTemperatureForThinking(payload)

	if got := gjson.GetBytes(out, "temperature").Float(); got != 0 {
		t.Fatalf("temperature = %v, want 0", got)
	}
}

func TestNormalizeClaudeTemperatureForThinking_AfterForcedToolChoiceKeepsOriginalTemperature(t *testing.T) {
	payload := []byte(`{"temperature":0,"thinking":{"type":"adaptive"},"output_config":{"effort":"max"},"tool_choice":{"type":"any"}}`)
	out := disableThinkingIfToolChoiceForced(payload)
	out = normalizeClaudeTemperatureForThinking(out)

	if gjson.GetBytes(out, "thinking").Exists() {
		t.Fatalf("thinking should be removed when tool_choice forces tool use")
	}
	if got := gjson.GetBytes(out, "temperature").Float(); got != 0 {
		t.Fatalf("temperature = %v, want 0", got)
	}
}

func TestDisableThinkingIfToolChoiceForced_ToolsWithoutForcedChoiceKeepThinking(t *testing.T) {
	payload := []byte(`{"thinking":{"type":"adaptive"},"output_config":{"effort":"medium"},"tools":[{"name":"browser_navigate","input_schema":{"type":"object"}}]}`)
	out := disableThinkingIfToolChoiceForced(payload)

	if got := gjson.GetBytes(out, "thinking.type").String(); got != "adaptive" {
		t.Fatalf("thinking.type = %q, want adaptive", got)
	}
	if got := gjson.GetBytes(out, "output_config.effort").String(); got != "medium" {
		t.Fatalf("output_config.effort = %q, want medium", got)
	}
}

func TestRemapOAuthToolNames_TitleCase_NoReverseNeeded(t *testing.T) {
	body := []byte(`{"tools":[{"name":"Bash","description":"Run shell commands","input_schema":{"type":"object","properties":{"cmd":{"type":"string"}}}}],"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	out, renamed := remapOAuthToolNames(body)
	if renamed {
		t.Fatalf("renamed = true, want false")
	}
	if got := gjson.GetBytes(out, "tools.0.name").String(); got != "Bash" {
		t.Fatalf("tools.0.name = %q, want %q", got, "Bash")
	}

	resp := []byte(`{"content":[{"type":"tool_use","id":"toolu_01","name":"Bash","input":{"cmd":"ls"}}]}`)
	reversed := resp
	if renamed {
		reversed = reverseRemapOAuthToolNames(resp)
	}
	if got := gjson.GetBytes(reversed, "content.0.name").String(); got != "Bash" {
		t.Fatalf("content.0.name = %q, want %q", got, "Bash")
	}
}

func TestRemapOAuthToolNames_Lowercase_ReverseApplied(t *testing.T) {
	body := []byte(`{"tools":[{"name":"bash","description":"Run shell commands","input_schema":{"type":"object","properties":{"cmd":{"type":"string"}}}}],"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	out, renamed, reverseMap := remapOAuthToolNamesForOAuth(body)
	if !renamed {
		t.Fatalf("renamed = false, want true")
	}
	if got := gjson.GetBytes(out, "tools.0.name").String(); got != "Bash" {
		t.Fatalf("tools.0.name = %q, want %q", got, "Bash")
	}

	resp := []byte(`{"content":[{"type":"tool_use","id":"toolu_01","name":"Bash","input":{"cmd":"ls"}}]}`)
	reversed := resp
	if renamed {
		reversed = reverseRemapOAuthToolNamesWithMap(resp, reverseMap)
	}
	if got := gjson.GetBytes(reversed, "content.0.name").String(); got != "bash" {
		t.Fatalf("content.0.name = %q, want %q", got, "bash")
	}
}

func TestRemapOAuthToolNames_PrefixesNonClaudeCodeTools(t *testing.T) {
	body := []byte(`{
		"tools": [
			{"name":"browser_navigate","description":"Open a URL","input_schema":{"type":"object"}},
			{"name":"bash","description":"Run shell commands","input_schema":{"type":"object"}},
			{"name":"mcp__chrome-devtools__click","description":"Click in Chrome","input_schema":{"type":"object"}},
			{"type":"web_search_20250305","name":"web_search"}
		],
		"tool_choice":{"type":"tool","name":"browser_navigate"},
		"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]
	}`)

	out, renamed := remapOAuthToolNames(body)
	if !renamed {
		t.Fatalf("renamed = false, want true for lowercase bash")
	}
	if got := gjson.GetBytes(out, "tools.#").Int(); got != 4 {
		t.Fatalf("tools.# = %d, want 4; body: %s", got, string(out))
	}
	if got := gjson.GetBytes(out, "tools.0.name").String(); got != "BrowserNavigate" {
		t.Fatalf("tools.0.name = %q, want BrowserNavigate", got)
	}
	if got := gjson.GetBytes(out, "tools.1.name").String(); got != "Bash" {
		t.Fatalf("tools.1.name = %q, want Bash", got)
	}
	if got := gjson.GetBytes(out, "tools.2.name").String(); got != "mcp__chrome-devtools__click" {
		t.Fatalf("tools.2.name = %q, want mcp__chrome-devtools__click", got)
	}
	if got := gjson.GetBytes(out, "tools.3.name").String(); got != "web_search" {
		t.Fatalf("tools.3.name = %q, want web_search", got)
	}
	if got := gjson.GetBytes(out, "tool_choice.name").String(); got != "BrowserNavigate" {
		t.Fatalf("tool_choice.name = %q, want BrowserNavigate", got)
	}
	if got := gjson.GetBytes(out, "tools.0.description").String(); got != "" {
		t.Fatalf("tools.0.description = %q, want stripped empty string", got)
	}
	if got := gjson.GetBytes(out, "tools.1.description").String(); got != "" {
		t.Fatalf("tools.1.description = %q, want stripped empty string", got)
	}
}

func TestRemapOAuthToolNames_DoesNotCorruptDescriptionPropertySchema(t *testing.T) {
	body := []byte(`{
		"tools": [
			{"name":"question","description":"Ask a question","input_schema":{"type":"object","properties":{"description":{"description":"Question description","type":"string"}},"required":["description"]}}
		],
		"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]
	}`)

	out, renamed, _ := remapOAuthToolNamesForOAuth(body)
	if !renamed {
		t.Fatalf("renamed = false, want true")
	}
	if got := gjson.GetBytes(out, "tools.0.description").String(); got != "" {
		t.Fatalf("tools.0.description = %q, want stripped empty string", got)
	}
	if got := gjson.GetBytes(out, "tools.0.input_schema.properties.description.type").String(); got != "string" {
		t.Fatalf("description property schema type = %q, want string; body: %s", got, string(out))
	}
	if got := gjson.GetBytes(out, "tools.0.input_schema.properties.description.description").String(); got != "" {
		t.Fatalf("description property annotation = %q, want stripped empty string", got)
	}
}

func TestRemapOAuthToolNames_PrefixedNamesReverseWithRequestMap(t *testing.T) {
	body := []byte(`{
		"tools": [
			{"name":"browser_navigate","input_schema":{"type":"object"}},
			{"name":"mcp_chrome_devtools_click","input_schema":{"type":"object"}}
		],
		"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]
	}`)

	out, renamed, reverseMap := remapOAuthToolNamesForOAuth(body)
	if !renamed {
		t.Fatalf("renamed = false, want true")
	}
	if got := gjson.GetBytes(out, "tools.0.name").String(); got != "BrowserNavigate" {
		t.Fatalf("tools.0.name = %q, want BrowserNavigate", got)
	}
	if got := gjson.GetBytes(out, "tools.1.name").String(); got != "ChromeDevtoolsClick" {
		t.Fatalf("single-underscore mcp_ tool should be normalized; got %q", got)
	}

	resp := []byte(`{"content":[{"type":"tool_use","name":"BrowserNavigate","id":"toolu_01","input":{}},{"type":"tool_use","name":"ChromeDevtoolsClick","id":"toolu_02","input":{}}]}`)
	reversed := reverseRemapOAuthToolNamesWithMap(resp, reverseMap)
	if got := gjson.GetBytes(reversed, "content.0.name").String(); got != "browser_navigate" {
		t.Fatalf("content.0.name = %q, want browser_navigate", got)
	}
	if got := gjson.GetBytes(reversed, "content.1.name").String(); got != "mcp_chrome_devtools_click" {
		t.Fatalf("single-underscore mcp_ response name should reverse to client name; got %q", got)
	}
}

func TestRemapOAuthToolNames_PrefixedStreamNamesReverseWithRequestMap(t *testing.T) {
	body := []byte(`{"tools":[{"name":"terminal","input_schema":{"type":"object"}}]}`)
	_, renamed, reverseMap := remapOAuthToolNamesForOAuth(body)
	if !renamed {
		t.Fatalf("renamed = false, want true")
	}

	line := []byte(`data: {"type":"content_block_start","content_block":{"type":"tool_use","name":"Terminal","id":"toolu_01"},"index":0}`)
	out := reverseRemapOAuthToolNamesFromStreamLineWithMap(line, reverseMap)
	if got := gjson.GetBytes(helps.JSONPayload(out), "content_block.name").String(); got != "terminal" {
		t.Fatalf("content_block.name = %q, want terminal; line: %s", got, string(out))
	}
}

func TestRemapOAuthToolNames_PrefixesEveryNonClaudeCodeTool(t *testing.T) {
	body := []byte(`{"tools":[{"name":"browser_navigate","input_schema":{"type":"object"}},{"name":"terminal","input_schema":{"type":"object"}}]}`)

	out, renamed := remapOAuthToolNames(body)
	if !renamed {
		t.Fatalf("renamed = false, want true")
	}
	if got := gjson.GetBytes(out, "tools.0.name").String(); got != "BrowserNavigate" {
		t.Fatalf("tools.0.name = %q, want BrowserNavigate", got)
	}
	if got := gjson.GetBytes(out, "tools.1.name").String(); got != "Terminal" {
		t.Fatalf("tools.1.name = %q, want Terminal", got)
	}
}

// TestApplyClaudeHeaders_NonAnthropicUpstream verifies that Anthropic-specific
// fingerprint headers are NOT forwarded when the upstream is not api.anthropic.com.
func TestApplyClaudeHeaders_NonAnthropicUpstream(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "https://api.xheai.cc/v1/messages?beta=true", nil)
	auth := &cliproxyauth.Auth{
		Attributes: map[string]string{
			"api_key":  "sk-test-key",
			"base_url": "https://api.xheai.cc",
		},
	}
	applyClaudeHeaders(req, auth, "sk-test-key", false, nil, nil)

	if got := req.Header.Get("Anthropic-Dangerous-Direct-Browser-Access"); got != "" {
		t.Errorf("Anthropic-Dangerous-Direct-Browser-Access = %q, want empty for non-Anthropic upstream", got)
	}
	if got := req.Header.Get("X-App"); got != "" {
		t.Errorf("X-App = %q, want empty for non-Anthropic upstream", got)
	}
	beta := req.Header.Get("Anthropic-Beta")
	if strings.Contains(beta, "claude-code-20250219") {
		t.Errorf("Anthropic-Beta contains claude-code-20250219 for non-Anthropic upstream: %q", beta)
	}
}

// TestApplyClaudeHeaders_AnthropicUpstream verifies that all Anthropic-specific
// headers ARE set when the upstream is api.anthropic.com (normal case).
func TestApplyClaudeHeaders_AnthropicUpstream(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages?beta=true", nil)
	auth := &cliproxyauth.Auth{
		Attributes: map[string]string{
			"api_key": "sk-ant-test",
		},
	}
	applyClaudeHeaders(req, auth, "sk-ant-test", false, nil, nil)

	if got := req.Header.Get("Anthropic-Dangerous-Direct-Browser-Access"); got != "true" {
		t.Errorf("Anthropic-Dangerous-Direct-Browser-Access = %q, want %q for Anthropic upstream", got, "true")
	}
	if got := req.Header.Get("X-App"); got != "cli" {
		t.Errorf("X-App = %q, want %q for Anthropic upstream", got, "cli")
	}
	beta := req.Header.Get("Anthropic-Beta")
	if !strings.Contains(beta, "claude-code-20250219") {
		t.Errorf("Anthropic-Beta missing claude-code-20250219 for Anthropic upstream: %q", beta)
	}
}
