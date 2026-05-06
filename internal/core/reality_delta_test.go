package core

import "testing"

func TestHallucinationCoefficientBounds(t *testing.T) {
	if got := hallucinationCoefficient("same", "same"); got != 0 {
		t.Fatalf("same coefficient = %v, want 0", got)
	}
	if got := hallucinationCoefficient("", "actual"); got != 1 {
		t.Fatalf("empty coefficient = %v, want 1", got)
	}
	if got := hallucinationCoefficient("abc", "xyz"); got <= 0.5 || got > 1 {
		t.Fatalf("different coefficient = %v, want >0.5 and <=1", got)
	}
}
