//go:build darwin

package svc

import (
	"strings"
	"testing"
)

func TestPlist(t *testing.T) {
	p := plist("/usr/local/bin/kith", "/Users/me/.kith & co")
	for _, w := range []string{"<string>daemon</string>", "<string>--home</string>", "/Users/me/.kith &amp; co", "RunAtLoad", "KeepAlive", "EnvironmentVariables", Label} {
		if !strings.Contains(p, w) {
			t.Fatalf("plist lacks %q", w)
		}
	}
}
