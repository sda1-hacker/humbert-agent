// humbert-data 提供离线用户数据备份、校验和恢复。恢复前必须完全退出桌面应用。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

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
		output := flags.String("output", "", "备份 ZIP 目标路径")
		_ = flags.Parse(os.Args[2:])
		if *output == "" {
			usage()
		}
		if err := databackup.Create(ctx, *root, *output); err != nil {
			fail(err)
		}
		fmt.Println("备份已创建:", *output)
	case "verify":
		flags := flag.NewFlagSet("verify", flag.ExitOnError)
		archive := flags.String("archive", "", "备份 ZIP 路径")
		_ = flags.Parse(os.Args[2:])
		if *archive == "" {
			usage()
		}
		if err := databackup.Verify(ctx, *archive); err != nil {
			fail(err)
		}
		fmt.Println("备份校验通过:", *archive)
	case "restore":
		flags := flag.NewFlagSet("restore", flag.ExitOnError)
		root := flags.String("data-dir", defaultRoot, "Humbert 数据目录")
		archive := flags.String("archive", "", "备份 ZIP 路径")
		offline := flags.Bool("offline", false, "确认 Humbert 桌面应用已完全退出")
		_ = flags.Parse(os.Args[2:])
		if *archive == "" || !*offline {
			usage()
		}
		rollback, err := databackup.Restore(ctx, *archive, *root)
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
	fmt.Fprintln(os.Stderr, "用法: humbert-data backup -output BACKUP.zip | verify -archive BACKUP.zip | restore -archive BACKUP.zip -offline")
	os.Exit(2)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "humbert-data:", err)
	os.Exit(1)
}
