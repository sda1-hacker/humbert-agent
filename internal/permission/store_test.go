package permission

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func commandRule(id string, action Action, executable, sandboxFingerprint string) Rule {
	identity := CapabilityIdentity{
		Version: CapabilityIdentityVersion, Kind: CapabilityCommand, Tool: "run_command", Risk: RiskExec,
		SandboxFingerprint: sandboxFingerprint, Command: "go", Executable: executable,
	}
	if action == ActionDeny {
		identity = identity.DenyScope()
	}
	return Rule{
		ID: id, AgentID: "agent-1", ToolName: "run_command", Action: action, Scope: GrantAgent,
		Identity: identity, CreatedAt: time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC),
	}
}

func TestStorePersistsAgentRulesAndReloads(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "permissions.json")
	store, err := NewStore(ctx, path)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	rule := commandRule("rule-1", ActionAllow, "/usr/bin/go", "sbx1:a")
	if err := store.Upsert(ctx, rule); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	reloaded, err := NewStore(ctx, path)
	if err != nil {
		t.Fatalf("reload NewStore() error = %v", err)
	}
	rules, err := reloaded.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(rules) != 1 || !rules[0].Identity.EqualExact(rule.Identity) {
		t.Fatalf("reloaded rule = %#v, want %#v", rules, rule)
	}
}

func TestStoreDeleteReturnsDomainError(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(ctx, filepath.Join(t.TempDir(), "permissions.json"))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := store.Delete(ctx, "missing"); !errors.Is(err, ErrRuleNotFound) {
		t.Fatalf("Delete() error = %v, want ErrRuleNotFound", err)
	}
}

func TestStoreUpsertReplacesLogicalTarget(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(ctx, filepath.Join(t.TempDir(), "permissions.json"))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	first := commandRule("allow-1", ActionAllow, "/usr/bin/go", "sbx1:a")
	second := commandRule("allow-2", ActionAllow, "/opt/go/bin/go", "sbx1:b")
	second.CreatedAt = first.CreatedAt.Add(time.Second)
	if err := store.Upsert(ctx, first); err != nil {
		t.Fatalf("保存 first 失败: %v", err)
	}
	if err := store.Upsert(ctx, second); err != nil {
		t.Fatalf("保存 second 失败: %v", err)
	}

	rules, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(rules) != 1 || rules[0].ID != second.ID {
		t.Fatalf("同一逻辑目标应被新规则替换，got %#v", rules)
	}
}

func TestDeleteByActionKeepsDenyRules(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(ctx, filepath.Join(t.TempDir(), "permissions.json"))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	allow := Rule{
		ID: "allow-1", AgentID: "agent-1", ToolName: "write_file", Action: ActionAllow, Scope: GrantAgent,
		Identity:  CapabilityIdentity{Version: CapabilityIdentityVersion, Kind: CapabilityBuiltin, Tool: "write_file", Risk: RiskWrite, SandboxFingerprint: "sbx1:a"},
		CreatedAt: time.Now().UTC(),
	}
	denyIdentity := CapabilityIdentity{Version: CapabilityIdentityVersion, Kind: CapabilityBuiltin, Tool: "edit_file", Risk: RiskWrite, SandboxFingerprint: "sbx1:a"}.DenyScope()
	deny := Rule{
		ID: "deny-1", AgentID: "agent-1", ToolName: "edit_file", Action: ActionDeny, Scope: GrantAgent,
		Identity: denyIdentity, CreatedAt: allow.CreatedAt.Add(time.Second),
	}
	for _, rule := range []Rule{allow, deny} {
		if err := store.Upsert(ctx, rule); err != nil {
			t.Fatalf("Upsert(%s) error = %v", rule.ID, err)
		}
	}

	deleted, err := store.DeleteByAction(ctx, ActionAllow)
	if err != nil {
		t.Fatalf("DeleteByAction() error = %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	rules, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(rules) != 1 || rules[0].Action != ActionDeny {
		t.Fatalf("长期 Deny 应保留，got %#v", rules)
	}
}

func TestStoreMigratesV1ByRevokingAllowsAndKeepingDenies(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "permissions.json")
	legacy := map[string]any{
		"version": 1,
		"rules": []any{
			map[string]any{
				"id": "old-allow", "agentId": "agent-1", "tool": "run_command", "action": "allow", "scope": "agent",
				"condition": map[string]any{"command": "python3"}, "createdAt": "2026-09-10T10:00:00Z",
			},
			map[string]any{
				"id": "old-deny", "agentId": "agent-1", "tool": "run_command", "action": "deny", "scope": "agent",
				"condition": map[string]any{"command": "python3"}, "createdAt": "2026-09-10T10:01:00Z",
			},
		},
	}
	raw, err := json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := NewStore(ctx, path)
	if err != nil {
		t.Fatalf("NewStore() migration error = %v", err)
	}
	rules, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0].ID != "old-deny" || rules[0].Action != ActionDeny {
		t.Fatalf("v1 migration rules = %#v, want only old deny", rules)
	}

	var header struct {
		Version int `json:"version"`
	}
	migrated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(migrated, &header); err != nil {
		t.Fatal(err)
	}
	if header.Version != policySchemaVersion {
		t.Fatalf("migrated version = %d, want %d", header.Version, policySchemaVersion)
	}
}
