package handlers

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

func TestGetRequestDetails_PreservesSuffix(t *testing.T) {
	modelRegistry := registry.GetGlobalRegistry()
	now := time.Now().Unix()

	modelRegistry.RegisterClient("test-request-details-gemini", "gemini", []*registry.ModelInfo{
		{ID: "gemini-2.5-pro", Created: now + 30},
		{ID: "gemini-2.5-flash", Created: now + 25},
	})
	modelRegistry.RegisterClient("test-request-details-openai", "openai", []*registry.ModelInfo{
		{ID: "gpt-5.2", Created: now + 20},
	})
	modelRegistry.RegisterClient("test-request-details-claude", "claude", []*registry.ModelInfo{
		{ID: "claude-sonnet-4-5", Created: now + 5},
	})

	// Ensure cleanup of all test registrations.
	clientIDs := []string{
		"test-request-details-gemini",
		"test-request-details-openai",
		"test-request-details-claude",
	}
	for _, clientID := range clientIDs {
		id := clientID
		t.Cleanup(func() {
			modelRegistry.UnregisterClient(id)
		})
	}

	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, coreauth.NewManager(nil, nil, nil))

	tests := []struct {
		name          string
		inputModel    string
		wantProviders []string
		wantModel     string
		wantErr       bool
	}{
		{
			name:          "numeric suffix preserved",
			inputModel:    "gemini-2.5-pro(8192)",
			wantProviders: []string{"gemini"},
			wantModel:     "gemini-2.5-pro(8192)",
			wantErr:       false,
		},
		{
			name:          "level suffix preserved",
			inputModel:    "gpt-5.2(high)",
			wantProviders: []string{"openai"},
			wantModel:     "gpt-5.2(high)",
			wantErr:       false,
		},
		{
			name:          "no suffix unchanged",
			inputModel:    "claude-sonnet-4-5",
			wantProviders: []string{"claude"},
			wantModel:     "claude-sonnet-4-5",
			wantErr:       false,
		},
		{
			name:          "unknown model with suffix",
			inputModel:    "unknown-model(8192)",
			wantProviders: nil,
			wantModel:     "",
			wantErr:       true,
		},
		{
			name:          "auto suffix resolved",
			inputModel:    "auto(high)",
			wantProviders: []string{"gemini"},
			wantModel:     "gemini-2.5-pro(high)",
			wantErr:       false,
		},
		{
			name:          "special suffix none preserved",
			inputModel:    "gemini-2.5-flash(none)",
			wantProviders: []string{"gemini"},
			wantModel:     "gemini-2.5-flash(none)",
			wantErr:       false,
		},
		{
			name:          "special suffix auto preserved",
			inputModel:    "claude-sonnet-4-5(auto)",
			wantProviders: []string{"claude"},
			wantModel:     "claude-sonnet-4-5(auto)",
			wantErr:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			providers, model, errMsg := handler.getRequestDetails(tt.inputModel)
			if (errMsg != nil) != tt.wantErr {
				t.Fatalf("getRequestDetails() error = %v, wantErr %v", errMsg, tt.wantErr)
			}
			if errMsg != nil {
				return
			}
			if !reflect.DeepEqual(providers, tt.wantProviders) {
				t.Fatalf("getRequestDetails() providers = %v, want %v", providers, tt.wantProviders)
			}
			if model != tt.wantModel {
				t.Fatalf("getRequestDetails() model = %v, want %v", model, tt.wantModel)
			}
		})
	}
}

func TestGetRequestDetails_ImageModelReturns503(t *testing.T) {
	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, coreauth.NewManager(nil, nil, nil))

	_, _, errMsg := handler.getRequestDetails("gpt-image-2")
	if errMsg == nil {
		t.Fatalf("expected error for gpt-image-2, got nil")
	}
	if errMsg.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("unexpected status code: got %d want %d", errMsg.StatusCode, http.StatusServiceUnavailable)
	}
	if errMsg.Error == nil {
		t.Fatalf("expected error message, got nil")
	}
	msg := errMsg.Error.Error()
	if !strings.Contains(msg, "/v1/images/generations") || !strings.Contains(msg, "/v1/images/edits") {
		t.Fatalf("unexpected error message: %q", msg)
	}
}

type requestedModelCaptureExecutor struct {
	model          string
	requestedModel string
}

func (e *requestedModelCaptureExecutor) Identifier() string { return "codex" }

func (e *requestedModelCaptureExecutor) Execute(_ context.Context, _ *coreauth.Auth, req coreexecutor.Request, opts coreexecutor.Options) (coreexecutor.Response, error) {
	e.model = req.Model
	if opts.Metadata != nil {
		e.requestedModel = strings.TrimSpace(fmt.Sprint(opts.Metadata[coreexecutor.RequestedModelMetadataKey]))
	}
	return coreexecutor.Response{Payload: []byte(`{"ok":true}`)}, nil
}

func (e *requestedModelCaptureExecutor) ExecuteStream(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	return nil, fmt.Errorf("stream not implemented")
}

func (e *requestedModelCaptureExecutor) Refresh(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}

func (e *requestedModelCaptureExecutor) CountTokens(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, fmt.Errorf("count not implemented")
}

func (e *requestedModelCaptureExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("http request not implemented")
}

func TestExecuteWithAuthManagerUsageMetadataKeepsRequestedAlias(t *testing.T) {
	modelRegistry := registry.GetGlobalRegistry()
	authID := "test-requested-model-alias-auth"
	modelRegistry.RegisterClient(authID, "codex", []*registry.ModelInfo{
		{ID: "codex-hermes", Created: time.Now().Unix()},
		{ID: "gpt-5.5", Created: time.Now().Unix()},
	})
	t.Cleanup(func() {
		modelRegistry.UnregisterClient(authID)
	})

	executor := &requestedModelCaptureExecutor{}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)
	manager.SetOAuthModelAlias(map[string][]internalconfig.OAuthModelAlias{
		"codex": {
			{Name: "gpt-5.5", Alias: "codex-hermes"},
		},
	})
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       authID,
		Provider: "codex",
		Status:   coreauth.StatusActive,
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	_, _, errMsg := handler.ExecuteWithAuthManager(
		context.Background(),
		"openai",
		"codex-hermes",
		[]byte(`{"model":"codex-hermes"}`),
		"",
	)
	if errMsg != nil {
		t.Fatalf("ExecuteWithAuthManager() error = %v", errMsg.Error)
	}
	if executor.model != "gpt-5.5" {
		t.Fatalf("executor model = %q, want upstream %q", executor.model, "gpt-5.5")
	}
	if executor.requestedModel != "codex-hermes" {
		t.Fatalf("requested model metadata = %q, want alias %q", executor.requestedModel, "codex-hermes")
	}
}
