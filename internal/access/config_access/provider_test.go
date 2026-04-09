package configaccess

import (
	"net/http/httptest"
	"testing"

	sdkaccess "github.com/router-for-me/CLIProxyAPI/v6/sdk/access"
)

func TestProviderAuthenticate_AcceptsAnyPresentedCredential(t *testing.T) {
	p := newProvider("test", []string{"configured-key"})

	req := httptest.NewRequest("GET", "http://example.com/v1/messages", nil)
	req.Header.Set("Authorization", "Bearer arbitrary-client-key")

	result, err := p.Authenticate(req.Context(), req)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if result == nil {
		t.Fatal("Authenticate() result = nil")
	}
	if result.Principal != "arbitrary-client-key" {
		t.Fatalf("Authenticate() principal = %q, want %q", result.Principal, "arbitrary-client-key")
	}
	if got := result.Metadata["source"]; got != "authorization" {
		t.Fatalf("Authenticate() source = %q, want %q", got, "authorization")
	}
}

func TestProviderAuthenticate_NoCredentials(t *testing.T) {
	p := newProvider("test", []string{"configured-key"})

	req := httptest.NewRequest("GET", "http://example.com/v1/messages", nil)

	result, err := p.Authenticate(req.Context(), req)
	if result != nil {
		t.Fatalf("Authenticate() result = %#v, want nil", result)
	}
	if err == nil {
		t.Fatal("Authenticate() error = nil, want no-credentials error")
	}
	if !sdkaccess.IsAuthErrorCode(err, sdkaccess.AuthErrorCodeNoCredentials) {
		t.Fatalf("Authenticate() error code = %q, want %q", err.Code, sdkaccess.AuthErrorCodeNoCredentials)
	}
}
