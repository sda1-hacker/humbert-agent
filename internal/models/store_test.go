package models

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/credential"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
)

func TestMultimediaConfigSurvivesModelMutations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	store, err := NewStore(ctx, filepath.Join(root, "providers.json"), filepath.Join(root, "models.json"))
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	provider := Provider{ID: "provider-1", Name: "Provider", Type: ProviderTypeOpenAI, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	image := Model{
		ID: "image-1", ProviderID: provider.ID, ModelName: "vision-model", DisplayName: "Vision",
		TimeoutMS: 1000, ContextWindow: 8192, MaxOutputTokens: 1024,
		Capabilities: CapabilityConfig{Vision: CapabilityEnabled}, Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateModel(ctx, image); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMultimediaConfig(ctx, MultimediaConfig{ImageModelID: image.ID}); err != nil {
		t.Fatal(err)
	}

	image.DisplayName = "Updated Vision"
	if err := store.UpdateModel(ctx, image); err != nil {
		t.Fatal(err)
	}
	config, err := store.MultimediaConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if config.ImageModelID != image.ID {
		t.Fatalf("image model id = %q, want %q", config.ImageModelID, image.ID)
	}
}

func TestRegistryValidatesAndProtectsConfiguredImageModel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	store, err := NewStore(ctx, filepath.Join(root, "providers.json"), filepath.Join(root, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := credential.New(filepath.Join(root, "secrets"))
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(store, credentials, logging.NewBootstrap())

	provider, err := registry.CreateProvider(ctx, CreateProviderInput{
		Name: "Provider", Type: ProviderTypeOpenAI, BaseURL: "https://example.invalid/v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	textModel, err := registry.CreateModel(ctx, CreateModelInput{
		ProviderID: provider.ID, ModelName: "text-only", DisplayName: "Text",
		TimeoutMS: 1000, ContextWindow: 8192, MaxOutputTokens: 1024,
		Capabilities: CapabilityConfig{Vision: CapabilityDisabled}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.SetMultimediaConfig(ctx, MultimediaConfig{ImageModelID: textModel.ID}); err == nil {
		t.Fatal("text-only model must not be accepted as image model")
	}

	imageModel, err := registry.CreateModel(ctx, CreateModelInput{
		ProviderID: provider.ID, ModelName: "vision-model", DisplayName: "Vision",
		TimeoutMS: 1000, ContextWindow: 8192, MaxOutputTokens: 1024,
		Capabilities: CapabilityConfig{Vision: CapabilityEnabled}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.SetMultimediaConfig(ctx, MultimediaConfig{ImageModelID: imageModel.ID}); err != nil {
		t.Fatal(err)
	}
	if err := registry.DeleteModel(ctx, imageModel.ID); !errors.Is(err, ErrModelInUse) {
		t.Fatalf("delete configured image model error = %v, want ErrModelInUse", err)
	}
	if _, err := registry.UpdateModel(ctx, imageModel.ID, UpdateModelInput{
		ProviderID: provider.ID, ModelName: imageModel.ModelName, DisplayName: imageModel.DisplayName,
		TimeoutMS: imageModel.TimeoutMS, ContextWindow: imageModel.ContextWindow,
		MaxOutputTokens: imageModel.MaxOutputTokens, Capabilities: imageModel.Capabilities, Enabled: false,
	}); err == nil {
		t.Fatal("configured image model must not be disabled")
	}
}
