package management

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
)

type usageExportPayload struct {
	Version    int                      `json:"version"`
	ExportedAt time.Time                `json:"exported_at"`
	Usage      usage.StatisticsSnapshot `json:"usage"`
}

type usageImportPayload struct {
	Version int                      `json:"version"`
	Usage   usage.StatisticsSnapshot `json:"usage"`
}

type usageCalibrationStore struct {
	Version      int              `json:"version"`
	UpdatedAt    time.Time        `json:"updated_at"`
	Calibrations []map[string]any `json:"calibrations"`
}

// GetUsageStatistics returns the in-memory request statistics snapshot.
func (h *Handler) GetUsageStatistics(c *gin.Context) {
	var snapshot usage.StatisticsSnapshot
	if h != nil && h.usageStats != nil {
		snapshot = h.usageStats.Snapshot()
	}
	c.JSON(http.StatusOK, gin.H{
		"usage":           snapshot,
		"failed_requests": snapshot.FailureCount,
	})
}

// ExportUsageStatistics returns a complete usage snapshot for backup/migration.
func (h *Handler) ExportUsageStatistics(c *gin.Context) {
	var snapshot usage.StatisticsSnapshot
	if h != nil && h.usageStats != nil {
		snapshot = h.usageStats.Snapshot()
	}
	c.JSON(http.StatusOK, usageExportPayload{
		Version:    1,
		ExportedAt: time.Now().UTC(),
		Usage:      snapshot,
	})
}

// ImportUsageStatistics merges a previously exported usage snapshot into memory.
func (h *Handler) ImportUsageStatistics(c *gin.Context) {
	if h == nil || h.usageStats == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "usage statistics unavailable"})
		return
	}

	data, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
		return
	}

	var payload usageImportPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}
	if payload.Version != 0 && payload.Version != 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported version"})
		return
	}

	result := h.usageStats.MergeSnapshot(payload.Usage)
	snapshot := h.usageStats.Snapshot()
	c.JSON(http.StatusOK, gin.H{
		"added":           result.Added,
		"skipped":         result.Skipped,
		"total_requests":  snapshot.TotalRequests,
		"failed_requests": snapshot.FailureCount,
	})
}

// GetUsageCalibrations returns persisted usage calibration records.
func (h *Handler) GetUsageCalibrations(c *gin.Context) {
	store, err := h.readUsageCalibrations()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, store)
}

// PostUsageCalibration appends one persisted usage calibration record.
func (h *Handler) PostUsageCalibration(c *gin.Context) {
	var calibration map[string]any
	if err := c.ShouldBindJSON(&calibration); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}
	if len(calibration) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "empty calibration"})
		return
	}

	now := time.Now().UTC()
	if _, ok := calibration["id"]; !ok {
		calibration["id"] = uuid.NewString()
	}
	if _, ok := calibration["recorded_at"]; !ok {
		calibration["recorded_at"] = now
	}

	store, err := h.readUsageCalibrations()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	store.Version = 1
	store.UpdatedAt = now
	store.Calibrations = append(store.Calibrations, calibration)

	if err := h.writeUsageCalibrations(store); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":          true,
		"calibration": calibration,
		"count":       len(store.Calibrations),
	})
}

func (h *Handler) usageCalibrationsPath() (string, error) {
	if h == nil || h.cfg == nil {
		return "", errors.New("config unavailable")
	}
	authDir, err := util.ResolveAuthDir(h.cfg.AuthDir)
	if err != nil {
		return "", err
	}
	if authDir == "" {
		return "", errors.New("auth-dir is not configured")
	}
	return filepath.Join(authDir, "calibrations.json"), nil
}

func (h *Handler) readUsageCalibrations() (usageCalibrationStore, error) {
	path, err := h.usageCalibrationsPath()
	if err != nil {
		return usageCalibrationStore{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return usageCalibrationStore{Version: 1, Calibrations: []map[string]any{}}, nil
		}
		return usageCalibrationStore{}, err
	}
	if len(data) == 0 {
		return usageCalibrationStore{Version: 1, Calibrations: []map[string]any{}}, nil
	}

	var store usageCalibrationStore
	if err := json.Unmarshal(data, &store); err != nil {
		return usageCalibrationStore{}, err
	}
	if store.Version == 0 {
		store.Version = 1
	}
	if store.Calibrations == nil {
		store.Calibrations = []map[string]any{}
	}
	return store, nil
}

func (h *Handler) writeUsageCalibrations(store usageCalibrationStore) error {
	path, err := h.usageCalibrationsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}
