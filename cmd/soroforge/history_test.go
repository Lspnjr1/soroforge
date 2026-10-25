package main

import (
	"testing"
	"time"
)

func TestParseSince(t *testing.T) {
	// Empty string returns zero time
	zero, err := parseSince("")
	if err != nil || !zero.IsZero() {
		t.Fatalf("expected zero time, got %v, err: %v", zero, err)
	}

	// Valid date string
	dateStr := "2026-09-01"
	parsedDate, err := parseSince(dateStr)
	if err != nil {
		t.Fatalf("failed to parse date: %v", err)
	}
	if parsedDate.Year() != 2026 || parsedDate.Month() != time.September || parsedDate.Day() != 1 {
		t.Fatalf("unexpected date parsed: %v", parsedDate)
	}

	// Valid duration string with days
	now := time.Now()
	parsedDays, err := parseSince("30d")
	if err != nil {
		t.Fatalf("failed to parse 30d: %v", err)
	}
	diff := now.Sub(parsedDays)
	if diff < 29*24*time.Hour || diff > 31*24*time.Hour {
		t.Fatalf("unexpected diff for 30d: %v", diff)
	}

	// Valid standard duration
	parsedHours, err := parseSince("24h")
	if err != nil {
		t.Fatalf("failed to parse 24h: %v", err)
	}
	diffH := now.Sub(parsedHours)
	if diffH < 23*time.Hour || diffH > 25*time.Hour {
		t.Fatalf("unexpected diff for 24h: %v", diffH)
	}

	// Invalid value rejected with helpful example
	_, err = parseSince("not-a-date")
	if err == nil {
		t.Fatal("expected error for invalid --since value, got nil")
	}
}

func TestParsePruneAge(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  time.Duration
	}{
		{input: "90d", want: 90 * 24 * time.Hour},
		{input: "2160h", want: 90 * 24 * time.Hour},
	} {
		got, err := parsePruneAge(tc.input)
		if err != nil {
			t.Fatalf("parsePruneAge(%q): %v", tc.input, err)
		}
		if got != tc.want {
			t.Errorf("parsePruneAge(%q) = %s, want %s", tc.input, got, tc.want)
		}
	}

	for _, input := range []string{"", "0d", "-1h", "not-a-duration"} {
		if _, err := parsePruneAge(input); err == nil {
			t.Errorf("parsePruneAge(%q) expected error", input)
		}
	}
}
