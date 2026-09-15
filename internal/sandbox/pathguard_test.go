package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWorkspaceOnlyPathGuardUsesOperationAccess(t *testing.T) {
	workspace := canonicalTempDir(t)
	outside := canonicalTempDir(t)
	policy := WorkspaceOnlyPolicy(workspace)

	inside := filepath.Join(workspace, "a.txt")
	if err := os.WriteFile(inside, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, op := range []PathOperation{OpRead, OpModify, OpDelete, OpMoveSource, OpMoveTarget} {
		decision, err := policy.CheckPath(inside, op)
		if err != nil {
			t.Fatalf("workspace %s should be allowed: %v", op, err)
		}
		if decision.Access != AccessFull || decision.Source != RuleSourceWorkspace {
			t.Fatalf("unexpected decision for %s: %#v", op, decision)
		}
	}
	if _, err := policy.CheckPath(filepath.Join(outside, "x.txt"), OpRead); err == nil {
		t.Fatal("outside read should be denied")
	}
	if _, err := policy.CheckPath(filepath.Join(outside, "x.txt"), OpModify); err == nil {
		t.Fatal("outside write should be denied")
	}
}

func TestReadWriteDoesNotGrantDeleteOrMove(t *testing.T) {
	workspace := canonicalTempDir(t)
	extra := canonicalTempDir(t)
	policy := WorkspaceOnlyPolicy(workspace)
	var err error
	policy, err = policy.WithPathAccess(extra, AccessReadWrite, RuleSourceAdditionalWrite)
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(extra, "new.txt")
	if _, err := policy.CheckPath(target, OpModify); err != nil {
		t.Fatalf("READ_WRITE modify should be allowed: %v", err)
	}
	for _, op := range []PathOperation{OpDelete, OpMoveSource, OpMoveTarget} {
		if _, err := policy.CheckPath(target, op); err == nil {
			t.Fatalf("READ_WRITE unexpectedly allowed %s", op)
		}
	}
}

func TestBlockedRuleAlwaysWinsOverParentGrant(t *testing.T) {
	home := canonicalTempDir(t)
	workspace := filepath.Join(home, "project")
	protected := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(protected, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(protected, "id_test")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	policy := EffectivePolicy{
		Profile:       ProfileStandard,
		NetworkMode:   NetworkPublic,
		NativeMode:    NativeOff,
		WorkspaceRoot: workspace,
		PathRules: sortedPathRules([]PathRule{
			{Root: home, Access: AccessReadOnly, Source: RuleSourceStandardHome},
			{Root: workspace, Access: AccessFull, Source: RuleSourceWorkspace},
			{Root: protected, Access: AccessBlocked, Source: RuleSourceProtected},
		}),
	}
	decision, err := policy.CheckPath(secret, OpRead)
	if err == nil {
		t.Fatal("protected child must override readable parent")
	}
	if decision.Access != AccessBlocked || decision.Source != RuleSourceProtected {
		t.Fatalf("unexpected blocked decision: %#v", decision)
	}
}

func TestSymlinkEscapeDenied(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require developer mode/admin")
	}
	workspace := canonicalTempDir(t)
	outside := canonicalTempDir(t)
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "escape")); err != nil {
		t.Fatal(err)
	}
	policy := WorkspaceOnlyPolicy(workspace)
	if _, err := policy.CheckPath(filepath.Join("escape", "secret.txt"), OpRead); err == nil {
		t.Fatal("symlink read escape should be denied")
	}
	if _, err := policy.CheckPath(filepath.Join("escape", "new.txt"), OpModify); err == nil {
		t.Fatal("symlink write escape should be denied")
	}
}

func TestFullAccessIsExplicitEscapeHatch(t *testing.T) {
	workspace := canonicalTempDir(t)
	outside := canonicalTempDir(t)
	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := EffectivePolicy{
		Profile:       ProfileFullAccess,
		NetworkMode:   NetworkAll,
		NativeMode:    NativeOff,
		WorkspaceRoot: workspace,
	}
	decision, err := policy.CheckPath(secret, OpDelete)
	if err != nil {
		t.Fatalf("full_access should fall back to OS user permissions: %v", err)
	}
	if decision.CanonicalPath != secret || decision.Access != AccessFull || decision.Source != RuleSourceUnrestricted {
		t.Fatalf("unexpected full access decision: %#v", decision)
	}
}

func TestNativeFilesystemIsDerivedFromPathRules(t *testing.T) {
	workspace := canonicalTempDir(t)
	readOnly := canonicalTempDir(t)
	readWrite := canonicalTempDir(t)
	blocked := filepath.Join(readOnly, "secret")
	blockedFile := filepath.Join(readOnly, ".netrc")
	if err := os.MkdirAll(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blockedFile, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := EffectivePolicy{
		Profile:       ProfileWorkspaceOnly,
		NetworkMode:   NetworkPublic,
		NativeMode:    NativePreferred,
		WorkspaceRoot: workspace,
		PathRules: sortedPathRules([]PathRule{
			{Root: workspace, Access: AccessFull, Source: RuleSourceWorkspace},
			{Root: readOnly, Access: AccessReadOnly, Source: RuleSourceStandardHome},
			{Root: readWrite, Access: AccessReadWrite, Source: RuleSourceAdditionalWrite},
			{Root: blocked, Access: AccessBlocked, Source: RuleSourceProtected},
			{Root: blockedFile, Access: AccessBlocked, Source: RuleSourceProtectedFile},
		}),
	}
	view := policy.NativeFilesystem()
	if !coveredByAnyRoot(readOnly, view.ReadOnlyRoots) {
		t.Fatalf("read-only root missing: %#v", view)
	}
	if !coveredByAnyRoot(workspace, view.WritableRoots) || !coveredByAnyRoot(readWrite, view.WritableRoots) {
		t.Fatalf("writable roots missing: %#v", view)
	}
	if !coveredByAnyRoot(blocked, view.BlockedRoots) {
		t.Fatalf("blocked root missing: %#v", view)
	}
	if !containsPath(view.BlockedFiles, blockedFile) {
		t.Fatalf("blocked file missing: %#v", view)
	}
}

func TestWithPathAccessCannotRegrantProtectedChild(t *testing.T) {
	workspace := canonicalTempDir(t)
	protected := canonicalTempDir(t)
	child := filepath.Join(protected, "child")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	policy := EffectivePolicy{
		Profile:       ProfileWorkspaceOnly,
		NetworkMode:   NetworkPublic,
		NativeMode:    NativeOff,
		WorkspaceRoot: workspace,
		PathRules: []PathRule{
			{Root: workspace, Access: AccessFull, Source: RuleSourceWorkspace},
			{Root: protected, Access: AccessBlocked, Source: RuleSourceProtected},
		},
	}
	if _, err := policy.WithPathAccess(child, AccessReadWrite, RuleSourceMCPFilesystem); err == nil {
		t.Fatal("runtime grant inside protected root must be rejected")
	}
}

func TestProtectedFileRuleMatchesExactFileOnly(t *testing.T) {
	home := canonicalTempDir(t)
	workspace := filepath.Join(home, "project")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(home, ".netrc")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := EffectivePolicy{
		Profile:       ProfileStandard,
		NetworkMode:   NetworkPublic,
		NativeMode:    NativeOff,
		WorkspaceRoot: workspace,
		PathRules: sortedPathRules([]PathRule{
			{Root: home, Access: AccessReadOnly, Source: RuleSourceStandardHome},
			{Root: workspace, Access: AccessFull, Source: RuleSourceWorkspace},
			{Root: secret, Access: AccessBlocked, Source: RuleSourceProtectedFile},
		}),
	}
	decision, err := policy.CheckPath(secret, OpRead)
	if err == nil || decision.Source != RuleSourceProtectedFile || decision.Access != AccessBlocked {
		t.Fatalf("protected file should be blocked: decision=%#v err=%v", decision, err)
	}
	normal := filepath.Join(home, "notes.txt")
	if err := os.WriteFile(normal, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := policy.CheckPath(normal, OpRead); err != nil {
		t.Fatalf("normal sibling should remain readable: %v", err)
	}
}

func containsPath(values []string, target string) bool {
	for _, value := range values {
		if pathEqual(value, target) {
			return true
		}
	}
	return false
}

// canonicalTempDir 与生产 Manager 一样使用真实路径，避免 macOS 临时目录别名改变规则匹配。
func canonicalTempDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}
