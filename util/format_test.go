package util

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

type testStruct struct {
	ID          int
	DisplayName string
	CreatedAt   time.Time
}

func TestFprintStruct(t *testing.T) {
	now := time.Now()
	ts := testStruct{
		ID:          1,
		DisplayName: "Test Item",
		CreatedAt:   now,
	}

	tests := []struct {
		name     string
		input    interface{}
		contains []string
	}{
		{
			name:  "simple struct (property view)",
			input: ts,
			contains: []string{
				"ID            1",
				"Display Name  Test Item",
				"Created       " + now.Format(time.RFC3339),
			},
		},
		{
			name:  "pointer to struct",
			input: &ts,
			contains: []string{
				"ID            1",
				"Display Name  Test Item",
				"Created       " + now.Format(time.RFC3339),
			},
		},
		{
			name:  "nil struct pointer",
			input: (*testStruct)(nil),
			contains: []string{
				"<nil>",
			},
		},
		{
			name:  "non-struct input",
			input: "not a struct",
			contains: []string{
				"not a struct",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			FprintStruct(&buf, tt.input)
			output := buf.String()

			for _, c := range tt.contains {
				if !strings.Contains(output, c) {
					t.Errorf("expected output to contain %q, but it was %q", c, output)
				}
			}
		})
	}
}

func TestFprintJSON(t *testing.T) {
	ts := testStruct{
		ID:          1,
		DisplayName: "Test Item",
	}

	var buf bytes.Buffer
	if err := FprintJSON(&buf, ts); err != nil {
		t.Fatalf("FprintJSON failed: %v", err)
	}

	output := buf.String()
	expected := `{
  "ID": 1,
  "DisplayName": "Test Item",
  "CreatedAt": "0001-01-01T00:00:00Z"
}
`
	if output != expected {
		t.Errorf("expected JSON:\n%q\ngot:\n%q", expected, output)
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{0, "0B"},
		{100, "100B"},
		{1024, "1.0KB"},
		{1024 * 1024, "1.0MB"},
		{1024 * 1024 * 1024, "1.0GB"},
		{1536, "1.5KB"},
	}

	for _, tt := range tests {
		got := FormatBytes(tt.input)
		if got != tt.expected {
			t.Errorf("FormatBytes(%d) = %q, expected %q", tt.input, got, tt.expected)
		}
	}
}

func TestFormatTimeAgo(t *testing.T) {
	now := time.Now()
	tests := []struct {
		input    time.Time
		expected string
	}{
		{time.Time{}, ""},
		{now.Add(-30 * time.Second), "just now"},
		{now.Add(-2 * time.Minute), "about 2 minutes ago"},
		{now.Add(-2 * time.Hour), "about 2 hours ago"},
		{now.Add(-2 * 24 * time.Hour), "about 2 days ago"},
		{now.Add(-45 * 24 * time.Hour), "about 1 month ago"},
		{now.Add(-400 * 24 * time.Hour), "about 1 year ago"},
	}

	for _, tt := range tests {
		got := FormatTimeAgo(tt.input)
		if got != tt.expected {
			t.Errorf("FormatTimeAgo(%v) = %q, expected %q", tt.input, got, tt.expected)
		}
	}
}

func TestFprintTable(t *testing.T) {
	type item struct {
		ID          int
		DisplayName string
		StatusShort string
	}

	items := []item{
		{ID: 1, DisplayName: "Item 1", StatusShort: "OK"},
		{ID: 2, DisplayName: "Item 2", StatusShort: "FAIL"},
	}

	var buf bytes.Buffer
	FprintTable(&buf, items)
	output := buf.String()

	if !strings.Contains(output, "ID") || !strings.Contains(output, "DISPLAY NAME") || !strings.Contains(output, "STATUS") {
		t.Errorf("expected headers not found in output:\n%s", output)
	}
	if !strings.Contains(output, "1") || !strings.Contains(output, "Item 1") || !strings.Contains(output, "OK") {
		t.Errorf("expected row 1 not found in output:\n%s", output)
	}
	if !strings.Contains(output, "2") || !strings.Contains(output, "Item 2") || !strings.Contains(output, "FAIL") {
		t.Errorf("expected row 2 not found in output:\n%s", output)
	}
}
