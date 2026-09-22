// humbert-data 提供离线用户数据备份、校验和恢复。恢复前必须完全退出桌面应用。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sda1-hacker/humbert-agent/internal/credential"
	"github.com/sda1-hacker/humbert-agent/internal/databackup"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fail(err)
	}
	defaultRoot := filepath.Join(home, ".humbert-agent")
	ctx := context.Background()
	switch os.Args[1] {
	case "backup":
		flags := flag.NewFlagSet("backup", flag.ExitOnError)
		root := flags.String("data-dir", defaultRoot, "Humbert 数据目录")
		output := flags.String("output", "", "加密备份目标路径（.age）")
		offline := flags.Bool("offline", false, "确认 Humbert 桌面应用已完全退出")
		passphraseFile := flags.String("passphrase-file", "", "备份口令文件路径")
		_ = flags.Parse(os.Args[2:])
		if *output == "" || *passphraseFile == "" || !*offline {
			usage()
		}
		passphrase, err := readPassphrase(*passphraseFile, *root)
		if err != nil {
			fail(err)
		}
		store, err := credential.NewSystem(filepath.Join(*root, "secrets"))
		if err != nil {
			fail(err)
		}
		values, err := store.ExportAll(ctx)
		if err != nil {
			fail(err)
		}
		if err := databackup.CreateEncrypted(ctx, *root, *output, passphrase, values); err != nil {
			fail(err)
		}
		fmt.Println("备份已创建:", *output)
	case "verify":
		flags := flag.NewFlagSet("verify", flag.ExitOnError)
		archive := flags.String("archive", "", "备份归档路径")
		passphraseFile := flags.String("passphrase-file", "", "加密备份口令文件路径")
		_ = flags.Parse(os.Args[2:])
		if *archive == "" {
			usage()
		}
		var verifyErr error
		if databackup.IsEncrypted(*archive) {
			passphrase, err := readPassphrase(*passphraseFile, "")
			if err != nil {
				fail(err)
			}
			verifyErr = databackup.VerifyEncrypted(ctx, *archive, passphrase)
		} else {
			verifyErr = databackup.Verify(ctx, *archive)
		}
		if verifyErr != nil {
			fail(verifyErr)
		}
		fmt.Println("备份校验通过:", *archive)
	case "restore":
		flags := flag.NewFlagSet("restore", flag.ExitOnError)
		root := flags.String("data-dir", defaultRoot, "Humbert 数据目录")
		archive := flags.String("archive", "", "备份归档路径")
		offline := flags.Bool("offline", false, "确认 Humbert 桌面应用已完全退出")
		passphraseFile := flags.String("passphrase-file", "", "加密备份口令文件路径")
		_ = flags.Parse(os.Args[2:])
		if *archive == "" || !*offline {
			usage()
		}
		var rollback string
		if databackup.IsEncrypted(*archive) {
			passphrase, readErr := readPassphrase(*passphraseFile, "")
			if readErr != nil {
				fail(readErr)
			}
			rollback, err = databackup.RestoreEncryptedAndImport(ctx, *archive, *root, passphrase, func(ctx context.Context, root string, values map[string]string) error {
				store, newErr := credential.NewSystem(filepath.Join(root, "secrets"))
				if newErr != nil {
					return newErr
				}
				return store.ImportAll(ctx, values)
			})
		} else {
			rollback, err = databackup.Restore(ctx, *archive, *root)
		}
		if err != nil {
			fail(err)
		}
		fmt.Println("数据已恢复:", *root)
		if rollback != "" {
			fmt.Println("原数据保留于:", rollback)
		}
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "用法: humbert-data backup -output BACKUP.age -passphrase-file FILE -offline | verify -archive BACKUP.age -passphrase-file FILE | restore -archive BACKUP.age -passphrase-file FILE -offline")
	os.Exit(2)
}

func readPassphrase(path, root string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("必须提供备份口令文件")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("口令文件必须是普通文件")
	}
	if info.Size() > 1024 {
		return "", fmt.Errorf("口令文件不能超过 1024 字节")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("口令文件权限必须为 0600 或更严格")
	}
	if root != "" {
		absoluteRoot, err := filepath.Abs(root)
		if err != nil {
			return "", err
		}
		if rel, err := filepath.Rel(absoluteRoot, absolute); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("口令文件不能位于数据目录内")
		}
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		return "", err
	}
	value := strings.TrimRight(string(data), "\r\n")
	if len([]rune(value)) < 12 || len(value) > 1024 {
		return "", fmt.Errorf("备份口令必须为 12-1024 字节")
	}
	return value, nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "humbert-data:", err)
	os.Exit(1)
}
