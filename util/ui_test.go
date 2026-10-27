package util

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

func TestProgressTracker(t *testing.T) {
	var buf bytes.Buffer
	totalBytes := int64(100)
	// Must start with "Download:" for ETA calculation to trigger speed window
	pt := NewProgressTracker(&buf, totalBytes, "Download: test")
	pt.Live = true

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

func TestConfirmRepeatedOnSameReader(t *testing.T) {
	// All answers arrive in one buffer; each Confirm call must consume only its own line.
	r := strings.NewReader("y\nn\nyes\n\nY")
	want := []bool{true, false, true, false, true, false}
	for i, w := range want {
		got, err := Confirm(io.Discard, r, "Continue?")
		if err != nil {
			t.Fatalf("prompt %d: unexpected error: %v", i, err)
		}
		if got != w {
			t.Errorf("prompt %d: got %v, want %v", i, got, w)
		}
	}
}

func TestProgressTrackerNotLive(t *testing.T) {
	var buf bytes.Buffer
	pt := NewProgressTracker(&buf, 2048, "Upload: evidence.bin")
	if pt.Live {
		t.Fatal("a buffer is not a terminal")
	}
	pt.SetTotalChunks(2)
	pt.Update(1024)
	pt.IncrementChunk()
	pt.Update(2048)
	if buf.Len() > 0 {
		t.Errorf("expected no output before Finish, got %q", buf.String())
	}
	pt.Finish()
	out := buf.String()
	if strings.ContainsAny(out, "\r\033") || strings.Count(out, "\n") != 1 || !strings.HasPrefix(out, "↑ evidence.bin ") || !strings.Contains(out, "2.0KB/2.0KB") {
		t.Errorf("expected one plain line, got %q", out)
	}
}
