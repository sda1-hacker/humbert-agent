package builtin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

var errFileChanged = errors.New("文件在操作期间已被修改，请重新读取后重试")

type fileLock struct {
	token chan struct{}
	refs  int
}

// 所有 Factory/会话共用文件锁；锁覆盖读取到提交，不能只保护最后一次 Rename。
var fileLocks = struct {
	sync.Mutex
	items map[string]*fileLock
}{items: make(map[string]*fileLock)}

func fileLockKey(path string) string {
	path = filepath.Clean(path)
	// 大小写不敏感卷上的别名也必须互斥；大小写敏感卷至多多串行化两个文件。
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		path = strings.ToLower(path)
	}
	return path
}

func lockFileTargets(ctx context.Context, targets ...*sandboxTarget) (func(), error) {
	keys := make([]string, 0, len(targets))
	seen := make(map[string]bool)
	for _, target := range targets {
		if target != nil {
			key := fileLockKey(target.absolute)
			if !seen[key] {
				seen[key] = true
				keys = append(keys, key)
			}
		}
	}
	// 多文件补丁、复制和移动统一按顺序取锁，避免反向操作互相等待。
	sort.Strings(keys)
	var releases []func()
	unlock := func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}
	for _, key := range keys {
		fileLocks.Lock()
		lock := fileLocks.items[key]
		if lock == nil {
			lock = &fileLock{token: make(chan struct{}, 1)}
			lock.token <- struct{}{}
			fileLocks.items[key] = lock
		}
		lock.refs++
		fileLocks.Unlock()
		releaseRef := func() {
			fileLocks.Lock()
			lock.refs--
			if lock.refs == 0 {
				delete(fileLocks.items, key)
			}
			fileLocks.Unlock()
		}
		select {
		case <-ctx.Done():
			releaseRef()
			unlock()
			return nil, ctx.Err()
		case <-lock.token:
			releases = append(releases, func() { lock.token <- struct{}{}; releaseRef() })
		}
	}
	if err := ctx.Err(); err != nil {
		unlock()
		return nil, err
	}
	return unlock, nil
}

// 外部编辑器不持有进程内锁，因此提交前还要校验文件身份、元数据和读改写原文。
// 这是提交前冲突检测；普通文件系统并不提供对外部进程的 compare-and-swap Rename。
type fileVersion struct {
	info    fs.FileInfo
	content []byte
}

func (v fileVersion) check(ctx context.Context, root *os.Root, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := root.Lstat(path)
	if v.info == nil && errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil || v.info == nil || !os.SameFile(v.info, info) ||
		v.info.Size() != info.Size() || v.info.Mode() != info.Mode() || !v.info.ModTime().Equal(info.ModTime()) {
		return fmt.Errorf("%w: %s", errFileChanged, path)
	}
	if v.content != nil {
		data, err := readRootFileWithLimit(ctx, root, path, int64(len(v.content)))
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || !bytes.Equal(data, v.content) {
			return fmt.Errorf("%w: %s", errFileChanged, path)
		}
	}
	return ctx.Err()
}
