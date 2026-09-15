package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func newTestManager(t *testing.T, home string) *Manager {
	t.Helper()
	manager, err := NewManager(Config{
		DefaultProfile:     ProfileWorkspaceOnly,
		DefaultNetworkMode: NetworkPublic,
		DefaultNativeMode:  NativePreferred,
		CommandGracePeriod: time.Second,
	}, home)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func TestResolveBuildsWorkspaceFullRule(t *testing.T) {
	appHome := t.TempDir()
	workspace := t.TempDir()
	manager := newTestManager(t, appHome)

	policy, err := manager.Resolve(context.Background(), workspace, AgentPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := CanonicalRoot(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if policy.Profile != ProfileWorkspaceOnly {
		t.Fatalf("profile = %q", policy.Profile)
	}
	decision, err := policy.CheckPath(canonical, OpDelete)
	if err != nil {
		t.Fatalf("workspace should be FULL: %v", err)
	}
	if decision.Access != AccessFull || !pathEqual(decision.MatchedRoot, canonical) || decision.Source != RuleSourceWorkspace {
		t.Fatalf("unexpected workspace decision: %#v", decision)
	}
}

func TestResolveAdditionalWritePathUsesReadWriteAccess(t *testing.T) {
	appHome := t.TempDir()
	workspace := t.TempDir()
	writable := t.TempDir()
	manager := newTestManager(t, appHome)

	policy, err := manager.Resolve(context.Background(), workspace, AgentPolicy{
		AdditionalWritePaths: []string{writable},
	})
	if err != nil {
		t.Fatal(err)
	}

	writeTarget := filepath.Join(writable, "new.txt")
	decision, err := policy.CheckPath(writeTarget, OpModify)
	if err != nil {
		t.Fatalf("additional write path denied: %v", err)
	}
	if decision.Access != AccessReadWrite || decision.Source != RuleSourceAdditionalWrite {
		t.Fatalf("unexpected additional write decision: %#v", decision)
	}
	if _, err := policy.CheckPath(writeTarget, OpDelete); err == nil {
		t.Fatal("READ_WRITE root must not grant delete/FULL")
	}
}

func TestResolveRejectsWorkspaceInsideProtectedRoot(t *testing.T) {
	appHome := t.TempDir()
	protectedWorkspace := filepath.Join(appHome, "secrets", "project")
	if err := os.MkdirAll(protectedWorkspace, 0o700); err != nil {
		t.Fatal(err)
	}
	manager := newTestManager(t, appHome)
	if _, err := manager.Resolve(context.Background(), protectedWorkspace, AgentPolicy{}); err == nil {
		t.Fatal("workspace inside protected root should be rejected")
	}
}

func TestRunnerNetworkNoneFailsClosedWithoutNativeNetworkIsolation(t *testing.T) {
	workspace := t.TempDir()
	manager := newTestManager(t, t.TempDir())
	policy := WorkspaceOnlyPolicy(workspace)
	policy.NetworkMode = NetworkNone
	policy.NativeMode = NativePreferred
	policy.Capability = Capability{
		Platform:  "test",
		Backend:   "none",
		Available: false,
		Reason:    "test backend unavailable",
	}

	_, err := manager.Runner().Run(context.Background(), policy, ProcessSpec{
		Executable: filepath.Join(string(filepath.Separator), "not-used"),
		Dir:        workspace,
	})
	if err == nil {
		t.Fatal("NetworkNone must fail closed when native network isolation is unavailable")
	}
}

func TestRunnerNetworkNoneRejectsNativeOff(t *testing.T) {
	workspace := t.TempDir()
	manager := newTestManager(t, t.TempDir())
	policy := WorkspaceOnlyPolicy(workspace)
	policy.NetworkMode = NetworkNone
	policy.NativeMode = NativeOff
	policy.Capability = Capability{
		Platform:  "test",
		Backend:   "test",
		Available: true,
		Network:   true,
	}

	_, err := manager.Runner().Run(context.Background(), policy, ProcessSpec{
		Executable: filepath.Join(string(filepath.Separator), "not-used"),
		Dir:        workspace,
	})
	if err == nil {
		t.Fatal("NetworkNone must fail closed when NativeMode is off")
	}
}

func TestUpdateConfigAffectsFutureResolveOnly(t *testing.T) {
	manager := newTestManager(t, t.TempDir())
	workspace := t.TempDir()

	before, err := manager.Resolve(context.Background(), workspace, AgentPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if before.Profile != ProfileWorkspaceOnly || before.NetworkMode != NetworkPublic {
		t.Fatalf("unexpected initial policy: %#v", before)
	}

	if err := manager.UpdateConfig(Config{
		DefaultProfile:     ProfileStandard,
		DefaultNetworkMode: NetworkNone,
		DefaultNativeMode:  NativeRequired,
		CommandGracePeriod: 2 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}

	after, err := manager.Resolve(context.Background(), workspace, AgentPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if after.Profile != ProfileStandard || after.NetworkMode != NetworkNone || after.NativeMode != NativeRequired {
		t.Fatalf("updated defaults not applied: %#v", after)
	}
	if before.Profile != ProfileWorkspaceOnly || before.NetworkMode != NetworkPublic {
		t.Fatal("already frozen policy changed after Manager.UpdateConfig")
	}
}

func TestProtectedPathRuleCandidatesIncludeSymlinkTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks may require elevated Windows privileges")
	}
	root := t.TempDir()
	target := filepath.Join(root, "actual-secret")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	logical := filepath.Join(root, "logical-secret")
	if err := os.Symlink(target, logical); err != nil {
		t.Fatal(err)
	}

	rules := appendProtectedPathRuleCandidates(nil, logical, RuleSourceProtected)
	absoluteLogical, _ := filepath.Abs(logical)
	resolvedTarget, _ := filepath.EvalSymlinks(logical)
	if !containsRulePath(rules, absoluteLogical, RuleSourceProtected) {
		t.Fatalf("protected rules missing logical path: %#v", rules)
	}
	if !containsRulePath(rules, resolvedTarget, RuleSourceProtected) {
		t.Fatalf("protected rules missing resolved symlink target: %#v", rules)
	}
}

func TestProtectedRulesIncludeSensitiveFilesAndHumbertControlPlane(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	appHome := filepath.Join(home, ".humbert-agent")
	if err := os.MkdirAll(appHome, 0o700); err != nil {
		t.Fatal(err)
	}
	rules := protectedRulesFor(appHome, home)

	for _, want := range []string{
		filepath.Join(home, ".netrc"),
		filepath.Join(home, ".git-credentials"),
		filepath.Join(home, ".npmrc"),
		filepath.Join(home, ".pgpass"),
		filepath.Join(home, ".config", "pypoetry", "auth.toml"),
		filepath.Join(home, ".zsh_history"),
		filepath.Join(appHome, "config.yaml"),
	} {
		if !containsRulePath(rules, want, RuleSourceProtectedFile) {
			t.Fatalf("protected file rule missing %q", want)
		}
	}
	for _, want := range []string{
		filepath.Join(home, ".ssh"),
		filepath.Join(home, ".aws"),
		filepath.Join(appHome, "secrets"),
		filepath.Join(appHome, "logs"),
	} {
		if !containsRulePath(rules, want, RuleSourceProtected) {
			t.Fatalf("protected directory rule missing %q", want)
		}
	}
}

func containsRulePath(rules []PathRule, target string, source RuleSource) bool {
	for _, rule := range rules {
		if pathEqual(rule.Root, target) && rule.Access == AccessBlocked && rule.Source == source {
			return true
		}
	}
	return false
}
