package logging

import (
	"testing"
	"time"
)

func TestRequestLogFileNameUsesRequestTimestampAndID(t *testing.T) {
	got := RequestLogFileName("/v1/responses?api_key=masked", time.Date(2026, 5, 14, 12, 34, 56, 0, time.UTC), "a1b2c3d4")
	want := "v1-responses-2026-05-14T123456-a1b2c3d4.log"
	if got != want {
		t.Fatalf("RequestLogFileName() = %q, want %q", got, want)
	}
}
