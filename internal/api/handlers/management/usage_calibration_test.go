package management

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func TestUsageCalibrationRoundTrip(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, nil)

	body := bytes.NewBufferString(`{
		"type": "usage_percent_token_weight_calibration",
		"provider": "codex",
		"model": "gpt-5.5",
		"weights": {"fresh_input": 2.5, "output": 10, "cached": 0.25},
		"sample": {"weighted_tokens": 123.5}
	}`)
	postRec := httptest.NewRecorder()
	postCtx, _ := gin.CreateTestContext(postRec)
	postCtx.Request = httptest.NewRequest(http.MethodPost, "/v0/management/usage/calibrations", body)
	postCtx.Request.Header.Set("Content-Type", "application/json")

	h.PostUsageCalibration(postCtx)

	if postRec.Code != http.StatusOK {
		t.Fatalf("expected post status %d, got %d with body %s", http.StatusOK, postRec.Code, postRec.Body.String())
	}

	data, err := os.ReadFile(filepath.Join(authDir, "calibrations.json"))
	if err != nil {
		t.Fatalf("expected calibrations.json to be written: %v", err)
	}

	var stored usageCalibrationStore
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("failed to decode calibrations.json: %v", err)
	}
	if stored.Version != 1 {
		t.Fatalf("expected version 1, got %d", stored.Version)
	}
	if len(stored.Calibrations) != 1 {
		t.Fatalf("expected one calibration, got %d", len(stored.Calibrations))
	}
	if id, ok := stored.Calibrations[0]["id"].(string); !ok || id == "" {
		t.Fatalf("expected generated id in calibration: %#v", stored.Calibrations[0])
	}

	getRec := httptest.NewRecorder()
	getCtx, _ := gin.CreateTestContext(getRec)
	getCtx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/usage/calibrations", nil)

	h.GetUsageCalibrations(getCtx)

	if getRec.Code != http.StatusOK {
		t.Fatalf("expected get status %d, got %d with body %s", http.StatusOK, getRec.Code, getRec.Body.String())
	}

	var fetched usageCalibrationStore
	if err := json.Unmarshal(getRec.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("failed to decode get response: %v", err)
	}
	if len(fetched.Calibrations) != 1 || fetched.Calibrations[0]["model"] != "gpt-5.5" {
		t.Fatalf("unexpected fetched calibrations: %#v", fetched.Calibrations)
	}
}
