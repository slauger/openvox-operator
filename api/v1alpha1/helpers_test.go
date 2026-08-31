package v1alpha1

import "testing"

func TestBoolValue(t *testing.T) {
	tr, fa := true, false
	tests := []struct {
		name string
		in   *bool
		def  bool
		want bool
	}{
		{"nil falls back to the default true", nil, true, true},
		{"nil falls back to the default false", nil, false, false},
		{"explicit true wins over default false", &tr, false, true},
		{"explicit false wins over default true", &fa, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BoolValue(tt.in, tt.def); got != tt.want {
				t.Errorf("BoolValue() = %v, want %v", got, tt.want)
			}
		})
	}
}
