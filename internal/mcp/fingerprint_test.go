package mcp

import "testing"

func TestServerFingerprintIgnoresDisplayNameButTracksConnectionIdentity(t *testing.T) {
	base := Server{
		Key:       "github",
		Name:      "GitHub",
		Transport: TransportStdio,
		Stdio:     &StdioConfig{Command: "npx", Args: []string{"-y", "server"}},
	}
	first := ServerFingerprint(base)

	renamed := base
	renamed.Name = "公司 GitHub"
	if got := ServerFingerprint(renamed); got != first {
		t.Fatalf("display name should not change fingerprint: %s != %s", got, first)
	}

	changed := base
	changed.Stdio = &StdioConfig{Command: "npx", Args: []string{"-y", "other-server"}}
	if got := ServerFingerprint(changed); got == first {
		t.Fatal("connection config change must change fingerprint")
	}
}
