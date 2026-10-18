package review

import (
	"strings"
	"testing"
)

func TestRedactAcrossLines(t *testing.T) {
	tests := []struct {
		in         string
		gone, kept []string
	}{
		{"call +966 55\n123 4567", []string{"123 4567"}, []string{"call"}},
		{"Prices:\n1500\n2000\n3000", nil, []string{"1500", "2000", "3000"}},
		{"0551234567\n0559876543", []string{"0551234567", "0559876543"}, nil},
	}
	for _, tt := range tests {
		got := redact(tt.in)
		for _, g := range tt.gone {
			if strings.Contains(got, g) {
				t.Errorf("redact(%q) = %q keeps %q, want it masked", tt.in, got, g)
			}
		}
		for _, k := range tt.kept {
			if !strings.Contains(got, k) {
				t.Errorf("redact(%q) = %q lost %q, want prices and dates on their own lines kept", tt.in, got, k)
			}
		}
	}
}
