package web

import "testing"

func TestBuildCSPLocksScriptSrcToHash(t *testing.T) {
	got := buildCSP(indexHTML)
	if got == "" {
		t.Fatal("buildCSP returned empty policy")
	}
	if want := "script-src 'unsafe-inline'"; contains(got, want) {
		t.Errorf("script-src must not allow unsafe-inline: %s", got)
	}
	for _, want := range []string{"script-src 'sha256-", "default-src 'none'"} {
		if !contains(got, want) {
			t.Errorf("CSP missing %q: %s", want, got)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
