package credential

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

var (
	// ErrNotFound 用于允许调用方通过 errors.Is 判断凭据不存在。
	ErrNotFound = errors.New(
		"凭据不存在",
	)

	credentialIDPattern = regexp.MustCompile(
		`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`,
	)
)

// Store 负责 Humbert 的敏感凭据本地存储。
//
// Provider 在 providers.json 中只保存 credential_id，真正 API Key 不进入
// 普通配置文件。
//
// v0.1 使用 ~/.humbert-agent/secrets 中的权限受限文件。
// 后续可以在完全不影响 Model Registry 的情况下替换为：
//   - macOS Keychain；
//   - Windows Credential Manager；
//   - Linux Secret Service。
type Store struct {
	root string
}

// New 创建 CredentialStore。
//
// root 应来自 config.Paths.SecretsDir，不能来自用户输入。
func New(root string) (*Store, error) {
	root = strings.TrimSpace(root)

	if root == "" {
		return nil, errors.New(
			"凭据目录不能为空",
		)
	}

	absoluteRoot, err :=
		filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf(
			"解析凭据目录失败: %w",
			err,
		)
	}

	absoluteRoot =
		filepath.Clean(absoluteRoot)

	if err := os.MkdirAll(
		absoluteRoot,
		0700,
	); err != nil {
		return nil, fmt.Errorf(
			"创建凭据目录失败: %w",
			err,
		)
	}

	rootInfo, err := os.Lstat(absoluteRoot)
	if err != nil {
		return nil, fmt.Errorf(
			"读取凭据目录状态失败: %w",
			err,
		)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New(
			"凭据目录不能是符号链接",
		)
	}
	if !rootInfo.IsDir() {
		return nil, errors.New(
			"凭据路径不是目录",
		)
	}

	if runtime.GOOS != "windows" {
		if err := os.Chmod(
			absoluteRoot,
			0700,
		); err != nil {
			return nil, fmt.Errorf(
				"设置凭据目录权限失败: %w",
				err,
			)
		}
	}

	return &Store{
		root: absoluteRoot,
	}, nil
}

// Put 新增或替换一个凭据。
//
// 使用临时文件 + rename，尽量避免应用异常退出时留下半写入文件。
func (s *Store) Put(
	ctx context.Context,
	id string,
	value string,
) error {
	if ctx == nil {
		return errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf(
			"保存凭据被取消: %w",
			err,
		)
	}

	target, err := s.pathFor(id)
	if err != nil {
		return err
	}
	if err := validateCredentialTarget(target, true); err != nil {
		return err
	}

	value = strings.TrimSpace(value)

	if value == "" {
		return errors.New(
			"凭据内容不能为空",
		)
	}

	temp, err := os.CreateTemp(
		s.root,
		".credential-*",
	)
	if err != nil {
		return fmt.Errorf(
			"创建凭据临时文件失败: %w",
			err,
		)
	}

	tempName := temp.Name()
	committed := false

	defer func() {
		_ = temp.Close()

		if !committed {
			_ = os.Remove(tempName)
		}
	}()

	if runtime.GOOS != "windows" {
		if err := temp.Chmod(
			0600,
		); err != nil {
			return fmt.Errorf(
				"设置凭据临时文件权限失败: %w",
				err,
			)
		}
	}

	if _, err := temp.WriteString(
		value,
	); err != nil {
		return fmt.Errorf(
			"写入凭据失败: %w",
			err,
		)
	}

	if err := temp.Sync(); err != nil {
		return fmt.Errorf(
			"同步凭据文件失败: %w",
			err,
		)
	}

	if err := temp.Close(); err != nil {
		return fmt.Errorf(
			"关闭凭据临时文件失败: %w",
			err,
		)
	}

	if err := replaceFile(
		tempName,
		target,
	); err != nil {
		return fmt.Errorf(
			"提交凭据失败: %w",
			err,
		)
	}

	if runtime.GOOS != "windows" {
		if err := os.Chmod(
			target,
			0600,
		); err != nil {
			return fmt.Errorf(
				"设置凭据文件权限失败: %w",
				err,
			)
		}
	}

	committed = true

	return nil
}

// Get 读取指定凭据。
func (s *Store) Get(
	ctx context.Context,
	id string,
) (string, error) {
	if ctx == nil {
		return "", errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf(
			"读取凭据被取消: %w",
			err,
		)
	}

	path, err := s.pathFor(id)
	if err != nil {
		return "", err
	}
	if err := validateCredentialTarget(path, false); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf(
				"%w: %s",
				ErrNotFound,
				id,
			)
		}
		return "", err
	}

	content, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(
			err,
			os.ErrNotExist,
		) {
			return "", fmt.Errorf(
				"%w: %s",
				ErrNotFound,
				id,
			)
		}

		return "", fmt.Errorf(
			"读取凭据失败: %w",
			err,
		)
	}

	value :=
		strings.TrimSpace(
			string(content),
		)

	if value == "" {
		return "", fmt.Errorf(
			"凭据文件为空: %s",
			id,
		)
	}

	return value, nil
}

// Delete 删除凭据。
//
// 删除不存在的凭据仍然视为成功，因此具备幂等性。
func (s *Store) Delete(
	ctx context.Context,
	id string,
) error {
	if ctx == nil {
		return errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf(
			"删除凭据被取消: %w",
			err,
		)
	}

	path, err := s.pathFor(id)
	if err != nil {
		return err
	}

	if err := os.Remove(path); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf(
			"删除凭据失败: %w",
			err,
		)
	}

	return nil
}

func (s *Store) pathFor(
	id string,
) (string, error) {
	id = strings.TrimSpace(id)

	if !credentialIDPattern.MatchString(id) {
		return "", fmt.Errorf(
			"非法凭据 ID: %q",
			id,
		)
	}

	target := filepath.Clean(
		filepath.Join(
			s.root,
			id+".secret",
		),
	)

	relative, err :=
		filepath.Rel(
			s.root,
			target,
		)
	if err != nil {
		return "", fmt.Errorf(
			"校验凭据路径失败: %w",
			err,
		)
	}

	if relative == ".." ||
		strings.HasPrefix(
			relative,
			".."+string(filepath.Separator),
		) {
		return "", errors.New(
			"凭据路径越界",
		)
	}

	return target, nil
}

// validateCredentialTarget 验证凭据文件不会通过符号链接逃逸。
//
// allowMissing=true 用于 Put：不存在的目标是正常创建场景；Get 则要求目标必须
// 已经存在。凭据文件只能是普通文件，目录、FIFO、Device 和 symlink 都视为损坏或
// 非法状态。
func validateCredentialTarget(path string, allowMissing bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		if allowMissing && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("凭据文件不能是符号链接")
	}
	if !info.Mode().IsRegular() {
		return errors.New("凭据路径不是普通文件")
	}
	return nil
}

func replaceFile(
	source string,
	destination string,
) error {
	// Unix 通常允许 Rename 直接替换目标。
	if err := os.Rename(
		source,
		destination,
	); err == nil {
		return nil
	}

	// Windows 对已存在目标文件的 Rename 行为不同，
	// 因此使用备份方式保证替换失败时仍尽可能恢复旧值。
	backup :=
		destination + ".backup"

	_ = os.Remove(backup)

	oldExists := false

	if _, err := os.Stat(destination); err == nil {
		oldExists = true

		if err := os.Rename(
			destination,
			backup,
		); err != nil {
			return fmt.Errorf(
				"备份旧凭据失败: %w",
				err,
			)
		}
	} else if !errors.Is(
		err,
		os.ErrNotExist,
	) {
		return fmt.Errorf(
			"检查旧凭据失败: %w",
			err,
		)
	}

	if err := os.Rename(
		source,
		destination,
	); err != nil {
		if oldExists {
			_ = os.Rename(
				backup,
				destination,
			)
		}

		return fmt.Errorf(
			"替换凭据失败: %w",
			err,
		)
	}

	if oldExists {
		if err := os.Remove(backup); err != nil &&
			!errors.Is(
				err,
				os.ErrNotExist,
			) {
			return fmt.Errorf(
				"清理凭据备份失败: %w",
				err,
			)
		}
	}

	return nil
}
