package controller

import (
	"testing"
	"time"
)

func TestCSRPollBackoff(t *testing.T) {
	tests := []struct {
		attempts int
		expected time.Duration
	}{
		{0, 5 * time.Second},
		{1, 5 * time.Second},
		{2, 5 * time.Second},
		{3, 30 * time.Second},
		{4, 30 * time.Second},
		{5, 30 * time.Second},
		{6, 2 * time.Minute},
		{7, 2 * time.Minute},
		{8, 2 * time.Minute},
		{9, 2 * time.Minute},
		{10, 5 * time.Minute},
		{11, 5 * time.Minute},
		{12, 5 * time.Minute},
	}

	for _, tt := range tests {
		got := csrPollBackoff(tt.attempts)
		if got != tt.expected {
			t.Errorf("csrPollBackoff(%d) = %v, want %v", tt.attempts, got, tt.expected)
		}
	}
}
