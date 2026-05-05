package util

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestProgressTracker(t *testing.T) {
	var buf bytes.Buffer
	totalBytes := int64(100)
	// Must start with "Download:" for ETA calculation to trigger speed window
	pt := NewProgressTracker(&buf, totalBytes, "Download: test")

	// Test initial display
	pt.display()
	output := buf.String()
	if !strings.Contains(output, "test") {
		t.Errorf("expected output to contain 'test', got %q", output)
	}
	if !strings.Contains(output, "0%") {
		t.Errorf("expected output to contain '0%%', got %q", output)
	}

	// Test update
	buf.Reset()
	pt.Update(50)
	output = buf.String()
	if !strings.Contains(output, "50%") {
		t.Errorf("expected output to contain '50%%', got %q", output)
	}

	// Test ETA calculation (mocking samples and speed)
	now := time.Now()
	pt.currentBytes = 50
	pt.samples = []progressSample{
		{timestamp: now.Add(-10 * time.Second), bytes: 0},
		{timestamp: now, bytes: 50},
	}
	pt.lastETACalc = now.Add(-2 * time.Second) // Force recalculation
	// Speed will be 50 / 10 = 5 bytes/s
	// Remaining is 50 bytes, so ETA should be 10s

	buf.Reset()
	pt.display()
	output = buf.String()
	if !strings.Contains(output, "ETA 10s") {
		t.Errorf("expected output to contain 'ETA 10s', got %q", output)
	}

	// Test finish
	buf.Reset()
	pt.Finish()
	output = buf.String()
	if !strings.HasSuffix(output, "\n") {
		t.Errorf("expected newline on finish, got %q", output)
	}
	if !strings.Contains(output, "test") {
		t.Errorf("expected output to contain 'test', got %q", output)
	}
}
