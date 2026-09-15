package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/sda1-hacker/humbert-agent/internal/logging"
)

func TestResolveManagedWorkspace(
	t *testing.T,
) {
	t.Parallel()

	ctx :=
		context.Background()

	manager, err :=
		NewManager(
			ctx,
			filepath.Join(
				t.TempDir(),
				"workspaces",
			),
			logging.NewBootstrap(),
		)

	if err != nil {
		t.Fatalf(
			"创建 WorkspaceManager 失败: %v",
			err,
		)
	}

	defer manager.Close()

	agentID :=
		uuid.NewString()

	result, err :=
		manager.Resolve(
			ctx,
			agentID,
			ModeManaged,
			"",
		)

	if err != nil {
		t.Fatalf(
			"Resolve Managed Workspace 失败: %v",
			err,
		)
	}

	if result.Mode !=
		ModeManaged {

		t.Fatalf(
			"Workspace Mode 错误: %s",
			result.Mode,
		)
	}

	info, err :=
		os.Stat(
			result.RootDir,
		)

	if err != nil {
		t.Fatalf(
			"Managed Workspace 不存在: %v",
			err,
		)
	}

	if !info.IsDir() {
		t.Fatal(
			"Managed Workspace 不是目录",
		)
	}
}

func TestResolveCustomWorkspace(
	t *testing.T,
) {
	t.Parallel()

	ctx :=
		context.Background()

	base :=
		t.TempDir()

	custom :=
		filepath.Join(
			base,
			"project",
		)

	if err :=
		os.MkdirAll(
			custom,
			0o700,
		); err != nil {

		t.Fatalf(
			"创建测试 Custom Workspace 失败: %v",
			err,
		)
	}

	manager, err :=
		NewManager(
			ctx,
			filepath.Join(
				base,
				"managed",
			),
			logging.NewBootstrap(),
		)

	if err != nil {
		t.Fatalf(
			"创建 WorkspaceManager 失败: %v",
			err,
		)
	}

	defer manager.Close()

	result, err :=
		manager.Resolve(
			ctx,
			uuid.NewString(),
			ModeCustom,
			custom,
		)

	if err != nil {
		t.Fatalf(
			"Resolve Custom Workspace 失败: %v",
			err,
		)
	}

	if result.Mode !=
		ModeCustom {

		t.Fatalf(
			"Workspace Mode 错误: %s",
			result.Mode,
		)
	}

	if result.ConfiguredPath !=
		custom {

		t.Fatalf(
			"ConfiguredPath 错误: got=%q want=%q",
			result.ConfiguredPath,
			custom,
		)
	}

	root, err :=
		manager.OpenRoot(
			ctx,
			result,
		)

	if err != nil {
		t.Fatalf(
			"打开 Custom Workspace Root 失败: %v",
			err,
		)
	}

	if err :=
		root.Close(); err != nil {

		t.Fatalf(
			"关闭 Workspace Root 失败: %v",
			err,
		)
	}
}

func TestCustomWorkspaceUnavailable(
	t *testing.T,
) {
	t.Parallel()

	ctx :=
		context.Background()

	manager, err :=
		NewManager(
			ctx,
			filepath.Join(
				t.TempDir(),
				"managed",
			),
			logging.NewBootstrap(),
		)

	if err != nil {
		t.Fatalf(
			"创建 WorkspaceManager 失败: %v",
			err,
		)
	}

	defer manager.Close()

	_, err =
		manager.Resolve(
			ctx,
			uuid.NewString(),
			ModeCustom,
			filepath.Join(
				t.TempDir(),
				"missing-project",
			),
		)

	if !errors.Is(
		err,
		ErrUnavailable,
	) {
		t.Fatalf(
			"应该返回 ErrUnavailable: %v",
			err,
		)
	}
}

func TestManagedWorkspaceRejectsSymlink(
	t *testing.T,
) {
	t.Parallel()

	ctx :=
		context.Background()

	base :=
		t.TempDir()

	manager, err :=
		NewManager(
			ctx,
			filepath.Join(
				base,
				"managed",
			),
			logging.NewBootstrap(),
		)

	if err != nil {
		t.Fatalf(
			"创建 WorkspaceManager 失败: %v",
			err,
		)
	}

	defer manager.Close()

	agentID :=
		uuid.NewString()

	managedPath, err :=
		manager.ManagedPath(
			agentID,
		)

	if err != nil {
		t.Fatalf(
			"计算 ManagedPath 失败: %v",
			err,
		)
	}

	outside :=
		filepath.Join(
			base,
			"outside",
		)

	if err :=
		os.MkdirAll(
			outside,
			0o700,
		); err != nil {

		t.Fatalf(
			"创建外部目录失败: %v",
			err,
		)
	}

	if err :=
		os.Symlink(
			outside,
			managedPath,
		); err != nil {

		t.Skipf(
			"当前平台无法创建 symlink: %v",
			err,
		)
	}

	_, err =
		manager.Resolve(
			ctx,
			agentID,
			ModeManaged,
			"",
		)

	if !errors.Is(
		err,
		ErrUnsafeWorkspace,
	) {
		t.Fatalf(
			"应该拒绝 Managed Workspace symlink: %v",
			err,
		)
	}
}

func TestOpenRootRejectsSymlinkEscape(
	t *testing.T,
) {
	t.Parallel()

	ctx :=
		context.Background()

	base :=
		t.TempDir()

	custom :=
		filepath.Join(
			base,
			"project",
		)

	outside :=
		filepath.Join(
			base,
			"outside",
		)

	if err :=
		os.MkdirAll(
			custom,
			0o700,
		); err != nil {

		t.Fatalf(
			"创建 project 失败: %v",
			err,
		)
	}

	if err :=
		os.MkdirAll(
			outside,
			0o700,
		); err != nil {

		t.Fatalf(
			"创建 outside 失败: %v",
			err,
		)
	}

	if err :=
		os.WriteFile(
			filepath.Join(
				outside,
				"secret.txt",
			),
			[]byte("secret"),
			0o600,
		); err != nil {

		t.Fatalf(
			"创建 secret.txt 失败: %v",
			err,
		)
	}

	link :=
		filepath.Join(
			custom,
			"escape",
		)

	if err :=
		os.Symlink(
			outside,
			link,
		); err != nil {

		t.Skipf(
			"当前平台无法创建 symlink: %v",
			err,
		)
	}

	manager, err :=
		NewManager(
			ctx,
			filepath.Join(
				base,
				"managed",
			),
			logging.NewBootstrap(),
		)

	if err != nil {
		t.Fatalf(
			"创建 WorkspaceManager 失败: %v",
			err,
		)
	}

	defer manager.Close()

	resolved, err :=
		manager.Resolve(
			ctx,
			uuid.NewString(),
			ModeCustom,
			custom,
		)

	if err != nil {
		t.Fatalf(
			"Resolve Custom Workspace 失败: %v",
			err,
		)
	}

	root, err :=
		manager.OpenRoot(
			ctx,
			resolved,
		)

	if err != nil {
		t.Fatalf(
			"OpenRoot 失败: %v",
			err,
		)
	}

	defer root.Close()

	_, err =
		root.Open(
			filepath.Join(
				"escape",
				"secret.txt",
			),
		)

	if err == nil {
		t.Fatal(
			"os.Root 不应该允许 symlink 逃逸 Workspace",
		)
	}
}

func TestCustomWorkspaceMustBeAbsolute(
	t *testing.T,
) {
	t.Parallel()

	ctx :=
		context.Background()

	manager, err :=
		NewManager(
			ctx,
			filepath.Join(
				t.TempDir(),
				"managed",
			),
			logging.NewBootstrap(),
		)

	if err != nil {
		t.Fatalf(
			"创建 WorkspaceManager 失败: %v",
			err,
		)
	}

	defer manager.Close()

	_, err =
		manager.Resolve(
			ctx,
			uuid.NewString(),
			ModeCustom,
			"./relative-project",
		)

	if !errors.Is(
		err,
		ErrInvalidPath,
	) {
		t.Fatalf(
			"相对 Custom Workspace 应返回 ErrInvalidPath: %v",
			err,
		)
	}
}
