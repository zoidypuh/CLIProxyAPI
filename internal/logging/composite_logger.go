package logging

import (
	"errors"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
)

// CompositeRequestLogger fans request logs out to multiple loggers.
type CompositeRequestLogger struct {
	loggers []RequestLogger
}

// NewCompositeRequestLogger returns a logger that writes to every non-nil logger.
func NewCompositeRequestLogger(loggers ...RequestLogger) RequestLogger {
	filtered := make([]RequestLogger, 0, len(loggers))
	for _, logger := range loggers {
		if logger != nil {
			filtered = append(filtered, logger)
		}
	}
	if len(filtered) == 1 {
		return filtered[0]
	}
	return &CompositeRequestLogger{loggers: filtered}
}

func (l *CompositeRequestLogger) IsEnabled() bool {
	if l == nil {
		return false
	}
	for _, logger := range l.loggers {
		if logger != nil && logger.IsEnabled() {
			return true
		}
	}
	return false
}

func (l *CompositeRequestLogger) LogRequest(url, method string, requestHeaders map[string][]string, body []byte, statusCode int, responseHeaders map[string][]string, response, websocketTimeline, apiRequest, apiResponse, apiWebsocketTimeline []byte, apiResponseErrors []*interfaces.ErrorMessage, requestID string, requestTimestamp, apiResponseTimestamp time.Time) error {
	if l == nil {
		return nil
	}
	var errs []error
	for _, logger := range l.loggers {
		if logger == nil {
			continue
		}
		if err := logger.LogRequest(url, method, requestHeaders, body, statusCode, responseHeaders, response, websocketTimeline, apiRequest, apiResponse, apiWebsocketTimeline, apiResponseErrors, requestID, requestTimestamp, apiResponseTimestamp); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (l *CompositeRequestLogger) LogRequestWithOptions(url, method string, requestHeaders map[string][]string, body []byte, statusCode int, responseHeaders map[string][]string, response, websocketTimeline, apiRequest, apiResponse, apiWebsocketTimeline []byte, apiResponseErrors []*interfaces.ErrorMessage, force bool, requestID string, requestTimestamp, apiResponseTimestamp time.Time) error {
	if l == nil {
		return nil
	}
	var errs []error
	for _, logger := range l.loggers {
		if logger == nil {
			continue
		}
		if loggerWithOptions, ok := logger.(interface {
			LogRequestWithOptions(string, string, map[string][]string, []byte, int, map[string][]string, []byte, []byte, []byte, []byte, []byte, []*interfaces.ErrorMessage, bool, string, time.Time, time.Time) error
		}); ok {
			if err := loggerWithOptions.LogRequestWithOptions(url, method, requestHeaders, body, statusCode, responseHeaders, response, websocketTimeline, apiRequest, apiResponse, apiWebsocketTimeline, apiResponseErrors, force, requestID, requestTimestamp, apiResponseTimestamp); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		if force || logger.IsEnabled() {
			if err := logger.LogRequest(url, method, requestHeaders, body, statusCode, responseHeaders, response, websocketTimeline, apiRequest, apiResponse, apiWebsocketTimeline, apiResponseErrors, requestID, requestTimestamp, apiResponseTimestamp); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (l *CompositeRequestLogger) LogStreamingRequest(url, method string, headers map[string][]string, body []byte, requestID string) (StreamingLogWriter, error) {
	if l == nil {
		return &NoOpStreamingLogWriter{}, nil
	}
	writers := make([]StreamingLogWriter, 0, len(l.loggers))
	var errs []error
	for _, logger := range l.loggers {
		if logger == nil || !logger.IsEnabled() {
			continue
		}
		writer, err := logger.LogStreamingRequest(url, method, headers, body, requestID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if writer != nil {
			writers = append(writers, writer)
		}
	}
	if len(writers) == 0 {
		return &NoOpStreamingLogWriter{}, errors.Join(errs...)
	}
	return &CompositeStreamingLogWriter{writers: writers}, errors.Join(errs...)
}

// SetEnabled forwards request-log toggles to child loggers that support it.
func (l *CompositeRequestLogger) SetEnabled(enabled bool) {
	if l == nil {
		return
	}
	for _, logger := range l.loggers {
		if setter, ok := logger.(interface{ SetEnabled(bool) }); ok {
			setter.SetEnabled(enabled)
		}
	}
}

// SetErrorLogsMaxFiles forwards error-log retention changes to child loggers.
func (l *CompositeRequestLogger) SetErrorLogsMaxFiles(maxFiles int) {
	if l == nil {
		return
	}
	for _, logger := range l.loggers {
		if setter, ok := logger.(interface{ SetErrorLogsMaxFiles(int) }); ok {
			setter.SetErrorLogsMaxFiles(maxFiles)
		}
	}
}

// CompositeStreamingLogWriter fans streaming log writes out to multiple writers.
type CompositeStreamingLogWriter struct {
	writers []StreamingLogWriter
}

func (w *CompositeStreamingLogWriter) WriteChunkAsync(chunk []byte) {
	for _, writer := range w.writers {
		writer.WriteChunkAsync(chunk)
	}
}

func (w *CompositeStreamingLogWriter) WriteStatus(status int, headers map[string][]string) error {
	var errs []error
	for _, writer := range w.writers {
		if err := writer.WriteStatus(status, headers); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (w *CompositeStreamingLogWriter) WriteAPIRequest(apiRequest []byte) error {
	var errs []error
	for _, writer := range w.writers {
		if err := writer.WriteAPIRequest(apiRequest); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (w *CompositeStreamingLogWriter) WriteAPIResponse(apiResponse []byte) error {
	var errs []error
	for _, writer := range w.writers {
		if err := writer.WriteAPIResponse(apiResponse); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (w *CompositeStreamingLogWriter) WriteAPIWebsocketTimeline(apiWebsocketTimeline []byte) error {
	var errs []error
	for _, writer := range w.writers {
		if err := writer.WriteAPIWebsocketTimeline(apiWebsocketTimeline); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (w *CompositeStreamingLogWriter) SetFirstChunkTimestamp(timestamp time.Time) {
	for _, writer := range w.writers {
		writer.SetFirstChunkTimestamp(timestamp)
	}
}

func (w *CompositeStreamingLogWriter) Close() error {
	var errs []error
	for _, writer := range w.writers {
		if err := writer.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
