package documenttext

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
)

// 缓存只复用转换结果，不缓存失败、取消或任何 API 凭据；升级 Tabula 或转换规则后自动失效。
const conversionVersion = "weknora-tabula-structures-v10"

// 图片识别规则未变化时保持独立版本，避免仅升级解析或分块就重复调用视觉模型。
const imageTextVersion = "weknora-tabula-pdfium-images-v9"

// ExtractImageText 与正文转换共用有界、可校验、可取消的缓存；profile 不得包含密钥。
// key 包含图片内容与识别器版本，同一进程并发导入相同图片只识别一次。
func ExtractImageText(ctx context.Context, data []byte, profile string, opts ParserOptions, recognize func(context.Context) (string, error)) (string, bool, error) {
	opts = opts.effective()
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(imageTextVersion+"\x00image-text\x00"+hash+"\x00"+profile)))
	release, err := acquireParse(ctx, opts.CacheDirectory, key)
	if err != nil {
		return "", false, err
	}
	defer release()
	if cached, ok := readParseCache(ctx, key, hash, opts); ok && strings.TrimSpace(cached.Markdown) != "" {
		return cached.Markdown, true, nil
	}
	text, err := recognize(ctx)
	if err != nil {
		return "", false, err
	}
	text = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"))
	result, err := checkedResult(Result{Markdown: text, ContentHash: hash}, opts)
	if err != nil {
		return "", false, err
	}
	if text == "" {
		return "", false, fmt.Errorf("document parser: image recognition returned empty text")
	}
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	writeParseCache(ctx, key, result, opts)
	return text, false, ctx.Err()
}

var parseLocks = struct {
	sync.Mutex
	keys map[string]chan struct{}
}{keys: make(map[string]chan struct{})}
var cacheWriteMu sync.Mutex

func parseCacheKey(hash, ext string, opts ParserOptions) string {
	version := "unknown"
	var settings []debug.BuildSetting
	if info, ok := debug.ReadBuildInfo(); ok {
		settings = info.Settings
		for _, dep := range info.Deps {
			if dep.Path == "github.com/tsawler/tabula" {
				version = dep.Version
				if dep.Replace != nil {
					version += fmt.Sprintf("/%s@%s", dep.Replace.Path, dep.Replace.Version)
				}
			}
		}
	}
	// 资源限额也参与键计算：不能通过旧缓存绕过后来收紧的解析预检。
	opts.CacheDirectory, opts.CacheMaxBytes = "", 0
	opts.Timeout = 0
	data, _ := json.Marshal([]any{conversionVersion, version, settings, hash, strings.ToLower(ext), opts, os.Getenv("TESSDATA_PREFIX")})
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func acquireParse(ctx context.Context, directory, key string) (func(), error) {
	if directory == "" {
		return func() {}, ctx.Err()
	}
	key = filepath.Clean(directory) + "\x00" + key
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		parseLocks.Lock()
		if pending := parseLocks.keys[key]; pending != nil {
			parseLocks.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-pending:
			}
			continue
		}
		pending := make(chan struct{})
		parseLocks.keys[key] = pending
		parseLocks.Unlock()
		return func() {
			parseLocks.Lock()
			delete(parseLocks.keys, key)
			close(pending)
			parseLocks.Unlock()
		}, nil
	}
}

type cachedParse struct {
	Checksum string
	Result   Result
}

func openCache(opts ParserOptions, create bool) (*os.Root, error) {
	if create {
		if err := os.MkdirAll(opts.CacheDirectory, 0o700); err != nil {
			return nil, err
		}
	}
	info, err := os.Lstat(opts.CacheDirectory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("document parser: cache must be a real directory")
	}
	return os.OpenRoot(opts.CacheDirectory)
}

func readParseCache(ctx context.Context, key, hash string, opts ParserOptions) (Result, bool) {
	if opts.CacheDirectory == "" || ctx.Err() != nil {
		return Result{}, false
	}
	root, err := openCache(opts, false)
	if err != nil {
		return Result{}, false
	}
	defer root.Close()
	file, err := root.Open("parse-" + key + ".json")
	if err != nil {
		return Result{}, false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > int64(parserResponseLimit(opts)) || info.Size() > cacheLimit(opts) {
		return Result{}, false
	}
	var value cachedParse
	decoder := json.NewDecoder(io.LimitReader(&contextReader{ctx, file}, int64(parserResponseLimit(opts))+1))
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF || ctx.Err() != nil {
		return Result{}, false
	}
	data, _ := json.Marshal(value.Result)
	if value.Checksum != fmt.Sprintf("%x", sha256.Sum256(data)) || value.Result.ContentHash != hash || len(value.Result.Markdown) > opts.MaxOutputBytes {
		return Result{}, false
	}
	if _, err := checkedResult(value.Result, opts); err != nil {
		return Result{}, false
	}
	value.Result.CacheHit = true
	return value.Result, true
}

func cacheLimit(opts ParserOptions) int64 {
	if opts.CacheMaxBytes == 0 {
		return DefaultOptions().CacheMaxBytes
	}
	return opts.CacheMaxBytes
}

// 缓存不可用时继续正常导入。临时文件与原子 rename 防止异常退出写出半截结果；只清理自己命名的文件。
func writeParseCache(ctx context.Context, key string, result Result, opts ParserOptions) {
	if opts.CacheDirectory == "" || ctx.Err() != nil || strings.TrimSpace(result.Markdown) == "" {
		return
	}
	cacheWriteMu.Lock()
	defer cacheWriteMu.Unlock()
	root, err := openCache(opts, true)
	if err != nil {
		return
	}
	defer root.Close()
	body, err := json.Marshal(result)
	if err != nil {
		return
	}
	data, err := json.Marshal(cachedParse{Checksum: fmt.Sprintf("%x", sha256.Sum256(body)), Result: result})
	if err != nil || int64(len(data)) > cacheLimit(opts) || len(data) > parserResponseLimit(opts) || ctx.Err() != nil {
		return
	}
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return
	}
	type entry struct {
		name string
		info os.FileInfo
	}
	var files []entry
	var total int64
	for _, e := range entries {
		if ctx.Err() != nil {
			return
		}
		if len(e.Name()) != 75 || !strings.HasPrefix(e.Name(), "parse-") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if _, err := hex.DecodeString(e.Name()[6:70]); err != nil {
			continue
		}
		if info, err := e.Info(); err == nil && info.Mode().IsRegular() {
			files = append(files, entry{e.Name(), info})
			total += info.Size()
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].info.ModTime().Before(files[j].info.ModTime()) })
	for _, e := range files {
		if total+int64(len(data)) <= cacheLimit(opts) || ctx.Err() != nil {
			break
		}
		if root.Remove(e.name) == nil {
			total -= e.info.Size()
		}
	}
	if total+int64(len(data)) > cacheLimit(opts) || ctx.Err() != nil {
		return
	}
	temporary := ".parse-" + rand.Text()
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	defer root.Remove(temporary)
	if _, err = io.Copy(file, &contextReader{ctx, bytes.NewReader(data)}); err != nil {
		return
	}
	if file.Sync() != nil || file.Close() != nil || ctx.Err() != nil {
		return
	}
	name := "parse-" + key + ".json"
	if err = root.Rename(temporary, name); err != nil {
		// Windows 下缓存替换失败只丢弃缓存；不删除原文件，也不影响索引发布。
		return
	}
}
