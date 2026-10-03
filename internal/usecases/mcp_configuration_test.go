package usecases

import (
	"context"
	"testing"

	"github.com/sda1-hacker/humbert-agent/internal/config"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	humbertmcp "github.com/sda1-hacker/humbert-agent/internal/mcp"
)

type memoryMCPCredentials map[string]string

func (s memoryMCPCredentials) Put(_ context.Context, id, secret string) error {
	s[id] = secret
	return nil
}
func (s memoryMCPCredentials) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	delete(s, id)
	return nil
}

func newMCPConfigurationTest(t *testing.T) (*MCPConfiguration, memoryMCPCredentials) {
	t.Helper()
	store, err := humbertmcp.NewStore(context.Background(), t.TempDir()+"/servers.json")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := humbertmcp.NewManager(store, config.MCPConfig{MaxToolsPerServer: 64}, logging.NewBootstrap())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	secrets := memoryMCPCredentials{}
	return NewMCPConfiguration(manager, secrets, logging.NewBootstrap()), secrets
}

func TestMCPCreateRollsBackServerAndPartiallyWrittenSecrets(t *testing.T) {
	usecase, secrets := newMCPConfigurationTest(t)
	_, err := usecase.Create(context.Background(), MCPConfigurationRequest{
		Key: "demo", Name: "Demo", Transport: string(humbertmcp.TransportStreamableHTTP), Endpoint: "https://example.com/mcp",
		BearerEnabled: true, BearerToken: "secret", Headers: []MCPCredentialInput{{Name: "X-Token"}},
	})
	if err == nil {
		t.Fatal("accepted missing new header credential")
	}
	servers, listErr := usecase.manager.List(context.Background())
	if listErr != nil || len(servers) != 0 || len(secrets) != 0 {
		t.Fatalf("partial create survived: servers=%v credentials=%d err=%v", servers, len(secrets), listErr)
	}
}

func TestMCPUpdatePreservesBlankSecretsAndCleansOnlyCommittedObsoleteReferences(t *testing.T) {
	usecase, secrets := newMCPConfigurationTest(t)
	ctx := context.Background()
	request := MCPConfigurationRequest{Key: "demo", Name: "Demo", Transport: string(humbertmcp.TransportStreamableHTTP), Endpoint: "https://example.com/mcp", BearerEnabled: true, BearerToken: "old"}
	server, err := usecase.Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	oldID := server.HTTP.BearerCredentialID
	request.BearerToken = ""
	retained, err := usecase.Update(ctx, server.ID, request)
	if err != nil || retained.HTTP.BearerCredentialID != oldID || secrets[oldID] != "old" {
		t.Fatalf("blank secret removed old reference: %#v %v", retained, err)
	}
	request.BearerToken = "new"
	request.Name = ""
	if _, err = usecase.Update(ctx, server.ID, request); err == nil {
		t.Fatal("accepted invalid server update")
	}
	if len(secrets) != 1 || secrets[oldID] != "old" {
		t.Fatal("failed update changed old secret or leaked a new secret")
	}
	request.Name = "Demo updated"
	updated, err := usecase.Update(ctx, server.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(secrets) != 1 || secrets[updated.HTTP.BearerCredentialID] != "new" {
		t.Fatal("committed update left obsolete credentials")
	}
	if err := usecase.Delete(ctx, server.ID); err != nil {
		t.Fatal(err)
	}
	if len(secrets) != 0 {
		t.Fatal("deleted server left credentials")
	}
}
