package helps

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

type reserveBillingTransport func(*http.Request) (*http.Response, error)

func (f reserveBillingTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func reserveAuth() *cliproxyauth.Auth {
	return &cliproxyauth.Auth{Provider: "xai", Metadata: map[string]any{
		"email": "thegismar@gmail.com", "auth_kind": "oauth", "access_token": "test-token",
	}}
}

func TestXAIQuotaReserveBillingDecisions(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		allow      bool
	}{
		{"below boundary", `{"config":{"creditUsagePercent":89.99}}`, 200, true},
		{"zero usage", `{"config":{"creditUsagePercent":0}}`, 200, true},
		{"at boundary", `{"config":{"creditUsagePercent":90}}`, 200, false},
		{"exhausted", `{"config":{"creditUsagePercent":100}}`, 200, false},
		{"over limit", `{"config":{"creditUsagePercent":101}}`, 200, false},
		{"negative", `{"config":{"creditUsagePercent":-1}}`, 200, false},
		{"missing", `{"config":{}}`, 200, false},
		{"null", `{"config":{"creditUsagePercent":null}}`, 200, false},
		{"wrong type", `{"config":{"creditUsagePercent":"0"}}`, 200, false},
		{"malformed", `{`, 200, false},
		{"trailing garbage", `{"config":{"creditUsagePercent":0}}bad`, 200, false},
		{"nonfinite", `{"config":{"creditUsagePercent":1e999}}`, 200, false},
		{"oversized", strings.Repeat(" ", 1<<20) + `{"config":{"creditUsagePercent":0}}`, 200, false},
		{"server failure", `private upstream error`, 503, false},
		{"unauthorized", `private upstream error`, 401, false},
		{"rate limited", `private upstream error`, 429, false},
		{"redirect", ``, 302, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", reserveBillingTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Location": {"https://unexpected.example/"}}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			}))
			err := CheckXAIQuotaReserve(ctx, nil, reserveAuth())
			if (err == nil) != tc.allow || calls != 1 {
				t.Fatalf("err=%v calls=%d, want allow=%v and one billing request", err, calls, tc.allow)
			}
			if err != nil && (strings.Contains(err.Error(), "test-token") || strings.Contains(err.Error(), "private upstream")) {
				t.Fatal("billing error leaked sensitive data")
			}
		})
	}
}

func TestXAIQuotaReserveOnlyTargetsNamedOAuthAccount(t *testing.T) {
	for _, tc := range []struct {
		name      string
		edit      func(*cliproxyauth.Auth)
		wantCheck bool
	}{
		{"case and whitespace", func(a *cliproxyauth.Auth) { a.Metadata["email"] = " TheGismar@Gmail.com " }, true},
		{"similar email", func(a *cliproxyauth.Auth) { a.Metadata["email"] = "other-thegismar@gmail.com" }, false},
		{"different domain", func(a *cliproxyauth.Auth) { a.Metadata["email"] = "thegismar@example.com" }, false},
		{"missing email", func(a *cliproxyauth.Auth) { delete(a.Metadata, "email") }, false},
		{"other provider", func(a *cliproxyauth.Auth) { a.Provider = "codex" }, false},
		{"API account", func(a *cliproxyauth.Auth) { a.Metadata["auth_kind"] = "api_key" }, false},
		{"API key override", func(a *cliproxyauth.Auth) { a.Attributes = map[string]string{"api_key": "api-key"} }, false},
		{"OAuth attribute", func(a *cliproxyauth.Auth) {
			delete(a.Metadata, "auth_kind")
			a.Attributes = map[string]string{"auth_kind": "oauth"}
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := reserveAuth()
			tc.edit(auth)
			calls := 0
			ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", reserveBillingTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"config":{"creditUsagePercent":95}}`))}, nil
			}))
			err := CheckXAIQuotaReserve(ctx, nil, auth)
			if (calls == 1) != tc.wantCheck || (err != nil) != tc.wantCheck {
				t.Fatalf("err=%v calls=%d, want check=%v", err, calls, tc.wantCheck)
			}
		})
	}
	if err := CheckXAIQuotaReserve(nil, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestXAIQuotaReserveChecksFreshQuotaAndRecoversAfterReset(t *testing.T) {
	calls := 0
	ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", reserveBillingTransport(func(r *http.Request) (*http.Response, error) {
		body := []string{`{"config":{"creditUsagePercent":89}}`, `{"config":{"creditUsagePercent":90}}`, `{"config":{"creditUsagePercent":0}}`}[calls]
		calls++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	}))
	for i, wantAllow := range []bool{true, false, true} {
		if err := CheckXAIQuotaReserve(ctx, nil, reserveAuth()); (err == nil) != wantAllow {
			t.Fatalf("request %d: err=%v want allow=%v", i, err, wantAllow)
		}
	}
}

func TestXAIQuotaReserveFailsClosedButPreservesCancellation(t *testing.T) {
	ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", reserveBillingTransport(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("private transport failure")
	}))
	if err := CheckXAIQuotaReserve(ctx, nil, reserveAuth()); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatalf("transport failure must block without leaking details: %v", err)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	if err := CheckXAIQuotaReserve(cancelCtx, nil, reserveAuth()); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	auth := reserveAuth()
	delete(auth.Metadata, "access_token")
	if err := CheckXAIQuotaReserve(ctx, nil, auth); err == nil {
		t.Fatal("missing token must not allow inference")
	}
}
