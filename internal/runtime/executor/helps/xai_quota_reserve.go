package helps

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const xaiReserveEmail = "thegismar@gmail.com"

// CheckXAIQuotaReserve preserves a hardcoded 10% Grok subscription reserve for
// thegismar. Check each new execution, including after restart: cached readings
// could spend the reserve when other clients are using the same subscription.
// In-flight requests and delayed upstream accounting can still overshoot it.
func CheckXAIQuotaReserve(ctx context.Context, cfg *config.Config, auth *cliproxyauth.Auth) error {
	if auth == nil || !strings.EqualFold(strings.TrimSpace(auth.Provider), "xai") {
		return nil
	}
	email, _ := auth.Metadata["email"].(string)
	kind := strings.TrimSpace(auth.Attributes["auth_kind"])
	if kind == "" {
		kind, _ = auth.Metadata["auth_kind"].(string)
	}
	if !strings.EqualFold(strings.TrimSpace(email), xaiReserveEmail) ||
		!strings.EqualFold(strings.TrimSpace(kind), "oauth") || strings.TrimSpace(auth.Attributes["api_key"]) != "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	unknown := xaiReserveError("xai: thegismar quota could not be verified; preserving 10% reserve")
	token, _ := auth.Metadata["access_token"].(string)
	if strings.TrimSpace(token) == "" {
		return unknown
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://cli-chat-proxy.grok.com/v1/billing?format=credits", nil)
	if err != nil {
		return unknown
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("Accept", "application/json")
	client := NewProxyAwareHTTPClient(ctx, cfg, auth, 0)
	// Billing must never redirect the account credential to another endpoint.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		if errCtx := ctx.Err(); errCtx != nil {
			return errCtx
		}
		return unknown
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.Debug("xai: failed to close quota reserve billing response")
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return unknown
	}
	const maxBillingBytes = 1 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBillingBytes+1))
	if err != nil {
		if errCtx := ctx.Err(); errCtx != nil {
			return errCtx
		}
		return unknown
	}
	var billing struct {
		Config struct {
			Used *float64 `json:"creditUsagePercent"`
		} `json:"config"`
	}
	if len(body) > maxBillingBytes || json.Unmarshal(body, &billing) != nil || billing.Config.Used == nil || *billing.Config.Used < 0 {
		return unknown
	}
	if *billing.Config.Used >= 90 {
		return xaiReserveError(fmt.Sprintf("xai: thegismar 10%% quota reserve reached (%.2f%% used)", *billing.Config.Used))
	}
	return nil
}

// Use the existing account-wide failover path. Recheck after a minute instead
// of pinning the account until the reported reset, which can change early.
type xaiReserveError string

func (e xaiReserveError) Error() string            { return string(e) }
func (e xaiReserveError) StatusCode() int          { return http.StatusTooManyRequests }
func (e xaiReserveError) IsCredentialScoped() bool { return true }
func (e xaiReserveError) RetryAfter() *time.Duration {
	delay := time.Minute
	return &delay
}
