package config

import (
	"testing"
	"time"
)

func TestInQuietHours(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2025, 1, 1, h, 30, 0, 0, time.Local) }
	tests := []struct {
		window string
		hour   int
		want   bool
	}{
		{"", 3, false},
		{"garbage", 3, false},
		{"1-5", 3, true},
		{"1-5", 5, false},
		{"23-7", 23, true},
		{"23-7", 2, true},
		{"23-7", 12, false},
		{"8-8", 8, false},
	}
	for _, tt := range tests {
		if got := (Config{QuietHours: tt.window}).InQuietHours(at(tt.hour)); got != tt.want {
			t.Errorf("InQuietHours(%q, %d:30) = %v, want %v", tt.window, tt.hour, got, tt.want)
		}
	}
}

func TestConcurrencyDefault(t *testing.T) {
	if got := (Config{}).Concurrency(); got != DefaultMaxConcurrent {
		t.Errorf("default concurrency = %d", got)
	}
	if got := (Config{MaxConcurrent: 5}).Concurrency(); got != 5 {
		t.Errorf("configured concurrency = %d", got)
	}
}
