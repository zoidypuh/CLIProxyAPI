package cliproxy

import (
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

func TestRegisterModelsForAuth_CodexOAuthPlanTypesExposeGPT55(t *testing.T) {
	tests := []struct {
		name      string
		planType  string
		wantModel bool
	}{
		{name: "pro", planType: "pro", wantModel: true},
		{name: "plus", planType: "plus", wantModel: true},
		{name: "team", planType: "team", wantModel: true},
		{name: "business", planType: "business", wantModel: true},
		{name: "go", planType: "go", wantModel: true},
		{name: "free", planType: "free", wantModel: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &Service{cfg: &config.Config{}}
			authID := "auth-codex-" + tt.planType
			reg := registry.GetGlobalRegistry()
			reg.UnregisterClient(authID)
			t.Cleanup(func() {
				reg.UnregisterClient(authID)
			})

			service.registerModelsForAuth(&coreauth.Auth{
				ID:       authID,
				Provider: "codex",
				Status:   coreauth.StatusActive,
				Attributes: map[string]string{
					"auth_kind": "oauth",
					"plan_type": tt.planType,
				},
			})

			models := reg.GetModelsForClient(authID)
			if hasModelID(models, "gpt-5.5") != tt.wantModel {
				t.Fatalf("plan_type=%q gpt-5.5 presence = %v, want %v", tt.planType, hasModelID(models, "gpt-5.5"), tt.wantModel)
			}
		})
	}
}

func TestRegisterModelsForAuth_ClaudeOAuthIncludesOpus47(t *testing.T) {
	service := &Service{cfg: &config.Config{}}
	authID := "auth-claude-opus-47"
	reg := registry.GetGlobalRegistry()
	reg.UnregisterClient(authID)
	t.Cleanup(func() {
		reg.UnregisterClient(authID)
	})

	service.registerModelsForAuth(&coreauth.Auth{
		ID:       authID,
		Provider: "claude",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"auth_kind": "oauth",
		},
	})

	models := reg.GetModelsForClient(authID)
	if !hasModelID(models, "claude-opus-4-7") {
		t.Fatalf("expected claude OAuth models to include %q", "claude-opus-4-7")
	}
}

func hasModelID(models []*registry.ModelInfo, want string) bool {
	for _, model := range models {
		if model == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(model.ID), want) {
			return true
		}
	}
	return false
}
