package v1alpha1

import (
	"testing"
)

func TestParseDurationToSeconds(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int64
		wantErr bool
	}{
		// Empty and whitespace
		{name: "empty string", input: "", want: 0},
		{name: "whitespace only", input: "   ", want: 0},

		// Plain numbers (interpreted as seconds)
		{name: "plain zero", input: "0", want: 0},
		{name: "plain number", input: "3600", want: 3600},
		{name: "negative number", input: "-1", want: -1},

		// Seconds
		{name: "seconds", input: "30s", want: 30},
		{name: "zero seconds", input: "0s", want: 0},

		// Minutes
		{name: "minutes", input: "5m", want: 300},
		{name: "one minute", input: "1m", want: 60},

		// Hours
		{name: "hours", input: "2h", want: 7200},
		{name: "one hour", input: "1h", want: 3600},

		// Days
		{name: "days", input: "1d", want: 86400},
		{name: "seven days", input: "7d", want: 604800},

		// Years
		{name: "one year", input: "1y", want: 365 * 86400},
		{name: "five years", input: "5y", want: 5 * 365 * 86400},

		// Whitespace trimming
		{name: "leading whitespace", input: "  10s", want: 10},
		{name: "trailing whitespace", input: "10s  ", want: 10},

		// Errors
		{name: "unknown unit", input: "10x", wantErr: true},
		{name: "invalid format", input: "abc", wantErr: true},
		{name: "single char non-number", input: "x", wantErr: true},
		{name: "unit without number", input: "s", wantErr: true},
		{name: "decimal number", input: "1.5h", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDurationToSeconds(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseDurationToSeconds(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("ParseDurationToSeconds(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}
