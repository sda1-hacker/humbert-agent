package sandbox

import "testing"

func TestFingerprintStableAndTracksBoundaryChanges(t *testing.T) {
	root := t.TempDir()
	policy := WorkspaceOnlyPolicy(root)
	policy.Capability = Capability{
		Platform: "darwin", Backend: "seatbelt", Available: true,
		Filesystem: true, ProcessTree: true, Network: true,
	}
	policy.NativeMode = NativeRequired

	first, err := policy.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint() error = %v", err)
	}
	second, err := policy.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint() second error = %v", err)
	}
	if first != second {
		t.Fatalf("Fingerprint() unstable: %q != %q", first, second)
	}

	changed := policy
	changed.NetworkMode = NetworkNone
	next, err := changed.Fingerprint()
	if err != nil {
		t.Fatalf("changed Fingerprint() error = %v", err)
	}
	if next == first {
		t.Fatal("NetworkMode 变化后 Fingerprint 不应保持不变")
	}
}

func TestFingerprintChangesWhenProtectedFilePolicyChanges(t *testing.T) {
	root := t.TempDir()
	policy := WorkspaceOnlyPolicy(root)
	policy.Capability = Capability{Platform: "darwin", Backend: "seatbelt", Available: true, Filesystem: true, ProcessTree: true, Network: true}
	policy.NativeMode = NativeRequired

	before, err := policy.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	changed := policy
	changed.PathRules = append([]PathRule(nil), policy.PathRules...)
	changed.PathRules = append(changed.PathRules, PathRule{
		Root:   root + "/.sensitive",
		Access: AccessBlocked,
		Source: RuleSourceProtectedFile,
	})
	after, err := changed.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("新增敏感文件规则后 Sandbox Fingerprint 必须变化")
	}
}
