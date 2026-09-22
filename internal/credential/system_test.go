package credential

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	keyring "github.com/zalando/go-keyring"
)

type fakeKeyring struct {
	values  map[string]string
	failure error
}

func (f *fakeKeyring) Set(_, id, value string) error {
	if f.failure != nil {
		return f.failure
	}
	f.values[id] = value
	return nil
}
func (f *fakeKeyring) Get(_, id string) (string, error) {
	if f.failure != nil {
		return "", f.failure
	}
	value, ok := f.values[id]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return value, nil
}
func (f *fakeKeyring) Delete(_, id string) error {
	if f.failure != nil {
		return f.failure
	}
	delete(f.values, id)
	return nil
}

func TestSystemStoreMigratesLegacyAndExports(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "secrets")
	legacy, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Put(ctx, "provider-one", "secret-value"); err != nil {
		t.Fatal(err)
	}
	backend := &fakeKeyring{values: map[string]string{}}
	store, err := newSystemWithBackend(root, backend)
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Get(ctx, "provider-one")
	if err != nil || value != "secret-value" {
		t.Fatalf("migrate=%q err=%v", value, err)
	}
	if _, err := os.Stat(filepath.Join(root, "provider-one.secret")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("旧明文仍存在: %v", err)
	}
	values, err := store.ExportAll(ctx)
	if err != nil || values["provider-one"] != "secret-value" {
		t.Fatalf("export=%v err=%v", values, err)
	}
	if err := store.Delete(ctx, "provider-one"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, "provider-one"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除后仍存在: %v", err)
	}
}

func TestSystemStoreDoesNotFallBackOnKeyringFailure(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "secrets")
	legacy, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Put(ctx, "provider-one", "secret-value"); err != nil {
		t.Fatal(err)
	}
	backend := &fakeKeyring{values: map[string]string{}, failure: errors.New("locked")}
	store, err := newSystemWithBackend(root, backend)
	if err == nil || store != nil {
		t.Fatal("系统凭据库故障时启动迁移必须失败")
	}
	store, err = New(root)
	if err != nil {
		t.Fatal(err)
	}
	store.system = backend
	if err := store.Put(ctx, "provider-two", "new-secret"); err == nil {
		t.Fatal("系统凭据库故障时不能写入明文")
	}
}
