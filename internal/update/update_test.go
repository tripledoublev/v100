package update

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestIsNewer(t *testing.T) {
	tests := []struct {
		current string
		latest  string
		want    bool
	}{
		{"v0.2.9", "v0.2.10", true},
		{"v0.2.10", "v0.2.10", false},
		{"v0.2.11", "v0.2.10", false},
		{"dev", "v0.2.10", true},
		{"0.2.9", "v0.2.10", true},
		{"v0.2.9", "0.2.10", true},
	}

	for _, tt := range tests {
		got := IsNewer(tt.current, tt.latest)
		if got != tt.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}

func TestTargetAsset(t *testing.T) {
	asset := TargetAsset()
	if asset == "" {
		t.Fatal("TargetAsset returned empty string")
	}
	if !strings.HasPrefix(asset, "v100-") {
		t.Errorf("TargetAsset %q does not start with v100-", asset)
	}

	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	want := fmt.Sprintf("v100-%s-%s%s", runtime.GOOS, runtime.GOARCH, ext)
	if asset != want {
		t.Errorf("TargetAsset() = %q, want %q", asset, want)
	}
}
