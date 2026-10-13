package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestKimiAndKimiAIOAuthRoutes(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "test-management-key")
	server := newTestServer(t)

	// Local: an empty management key is a successful login. A wrong key still
	// must be rejected.
	wKimi := httptest.NewRecorder()
	reqKimi := httptest.NewRequest(http.MethodGet, "/v0/management/kimi-auth-url", nil)
	reqKimi.Header.Set("Authorization", "Bearer wrong-key")
	server.engine.ServeHTTP(wKimi, reqKimi)
	if wKimi.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-key kimi login route: %d", wKimi.Code)
	}

	wKimiAI := httptest.NewRecorder()
	reqKimiAI := httptest.NewRequest(http.MethodGet, "/v0/management/kimi-ai-auth-url", nil)
	reqKimiAI.Header.Set("Authorization", "Bearer wrong-key")
	server.engine.ServeHTTP(wKimiAI, reqKimiAI)
	if wKimiAI.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-key kimi-ai login route: %d", wKimiAI.Code)
	}

	registeredKimi := false
	registeredKimiAI := false
	for _, route := range server.engine.Routes() {
		if route.Method == http.MethodGet && route.Path == "/v0/management/kimi-auth-url" {
			registeredKimi = true
		}
		if route.Method == http.MethodGet && route.Path == "/v0/management/kimi-ai-auth-url" {
			registeredKimiAI = true
		}
	}
	if !registeredKimi {
		t.Fatal("Kimi login route not registered")
	}
	if !registeredKimiAI {
		t.Fatal("Kimi.ai login route not registered")
	}
}
