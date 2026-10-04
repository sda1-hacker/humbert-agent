// Package documentparse converts authorized document snapshots in a disposable
// subprocess. Callers select their own attachment or knowledge-base limits.
package documentparse

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tsawler/tabula"
	tabularag "github.com/tsawler/tabula/rag"
)

const workerMarker = "HUMBERT_DOCUMENT_PARSE_WORKER_V1"

var ErrResourceLimit = errors.New("document parser: resource limit exceeded")

type Options struct {
	MaxInputBytes            int64
	MaxOutputBytes           int
	MaxExpandedBytes         uint64
	MaxArchiveEntries        int
	Timeout                  time.Duration
	ExcludeHeadersAndFooters bool
	OCRLanguage              string
	IncludePageNumbers       bool
}

func DefaultOptions() Options {
	return Options{MaxInputBytes: 32 << 20, MaxOutputBytes: 32 << 20, MaxExpandedBytes: 256 << 20, MaxArchiveEntries: 8192, Timeout: time.Minute}
}

func (o Options) Validate() error {
	if o.MaxInputBytes < 0 || o.MaxOutputBytes < 0 || o.Timeout < 0 || o.MaxArchiveEntries < 0 {
		return errors.New("document parser: negative resource limit")
	}
	if o.MaxOutputBytes > int(^uint(0)>>1)/8 || o.MaxInputBytes == int64(^uint64(0)>>1) {
		return errors.New("document parser: resource limit too large")
	}
	return nil
}

func (o Options) effective() Options {
	d := DefaultOptions()
	if o.MaxInputBytes == 0 {
		o.MaxInputBytes = d.MaxInputBytes
	}
	if o.MaxOutputBytes == 0 {
		o.MaxOutputBytes = d.MaxOutputBytes
	}
	if o.MaxExpandedBytes == 0 {
		o.MaxExpandedBytes = d.MaxExpandedBytes
	}
	if o.MaxArchiveEntries == 0 {
		o.MaxArchiveEntries = d.MaxArchiveEntries
	}
	if o.Timeout == 0 {
		o.Timeout = d.Timeout
	}
	return o
}

type Result struct {
	Markdown    string
	Warnings    []tabula.Warning
	ContentHash string
}
type workerRequest struct {
	Path    string
	Options Options
}
type workerResponse struct {
	Result          Result
	Error           string
	ResourceLimited bool
}

func init() {
	if os.Getenv(workerMarker) != "1" {
		return
	}
	var req workerRequest
	if err := json.NewDecoder(io.LimitReader(os.Stdin, 64<<10)).Decode(&req); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	result, err := convert(req.Path, req.Options)
	response := workerResponse{Result: result}
	if err != nil {
		response.Error = err.Error()
		response.ResourceLimited = errors.Is(err, ErrResourceLimit)
	}
	if err = json.NewEncoder(os.Stdout).Encode(response); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

// ExtractFile copies the already-authorized source before validating and
// parsing, so file replacement cannot invalidate the preflight checks.
func ExtractFile(ctx context.Context, path string, opts Options) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := opts.Validate(); err != nil {
		return Result{}, err
	}
	opts = opts.effective()
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	info, err := os.Stat(path)
	if err != nil {
		return Result{}, err
	}
	if !info.Mode().IsRegular() {
		return Result{}, errors.New("document parser: source must be a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return Result{}, err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return Result{}, err
	}
	if !info.Mode().IsRegular() {
		return Result{}, errors.New("document parser: source must be a regular file")
	}
	if info.Size() > opts.MaxInputBytes {
		return Result{}, fmt.Errorf("%w: input exceeds %d bytes", ErrResourceLimit, opts.MaxInputBytes)
	}
	snapshot, err := os.CreateTemp("", "humbert-parser-*"+strings.ToLower(filepath.Ext(path)))
	if err != nil {
		return Result{}, err
	}
	defer os.Remove(snapshot.Name())
	defer snapshot.Close()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(snapshot, hash), io.LimitReader(&contextReader{ctx: ctx, reader: file}, opts.MaxInputBytes+1))
	if err != nil {
		return Result{}, err
	}
	if n > opts.MaxInputBytes {
		return Result{}, fmt.Errorf("%w: input exceeds %d bytes", ErrResourceLimit, opts.MaxInputBytes)
	}
	if err = snapshot.Close(); err != nil {
		return Result{}, err
	}
	if err = preflight(snapshot.Name(), opts); err != nil {
		return Result{}, err
	}
	if err = ctx.Err(); err != nil {
		return Result{}, err
	}
	executable, err := os.Executable()
	if err != nil {
		return Result{}, err
	}
	request, err := json.Marshal(workerRequest{Path: snapshot.Name(), Options: opts})
	if err != nil {
		return Result{}, err
	}
	command := exec.CommandContext(ctx, executable)
	command.Env = []string{workerMarker + "=1", "PATH=" + os.Getenv("PATH")}
	command.Stdin = bytes.NewReader(request)
	command.WaitDelay = time.Second
	output := &boundedWriter{limit: opts.MaxOutputBytes*6 + 64*4096*6 + (64 << 10)}
	stderr := &boundedWriter{limit: 4096}
	command.Stdout, command.Stderr = output, stderr
	if err = command.Run(); err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{}, fmt.Errorf("document parser: worker failed: %s: %w", strings.TrimSpace(stderr.String()), err)
	}
	if output.exceeded {
		return Result{}, fmt.Errorf("%w: worker response too large", ErrResourceLimit)
	}
	var response workerResponse
	if err = json.Unmarshal(output.Bytes(), &response); err != nil {
		return Result{}, fmt.Errorf("document parser: invalid worker response: %w", err)
	}
	if response.ResourceLimited {
		return Result{}, fmt.Errorf("%w: %s", ErrResourceLimit, response.Error)
	}
	if response.Error != "" {
		return Result{}, fmt.Errorf("document parser: %s", response.Error)
	}
	result := response.Result
	if len(result.Markdown) > opts.MaxOutputBytes {
		return Result{}, fmt.Errorf("%w: markdown exceeds %d bytes", ErrResourceLimit, opts.MaxOutputBytes)
	}
	result.ContentHash = fmt.Sprintf("%x", hash.Sum(nil))
	return result, nil
}

func convert(path string, opts Options) (Result, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".md" || ext == ".txt" || ext == ".csv" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Result{}, err
		}
		data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
		if !utf8.Valid(data) || bytes.ContainsRune(data, 0) {
			return Result{}, errors.New("document parser: text must be UTF-8 without NUL bytes")
		}
		if len(data) > opts.MaxOutputBytes {
			return Result{}, ErrResourceLimit
		}
		return Result{Markdown: string(data)}, nil
	}
	extractor := tabula.Open(path)
	if opts.ExcludeHeadersAndFooters {
		extractor = extractor.ExcludeHeadersAndFooters()
	}
	if opts.OCRLanguage != "" {
		extractor = extractor.OCRLanguage(opts.OCRLanguage)
	}
	markdownOpts := tabularag.DefaultMarkdownOptions()
	markdownOpts.IncludePageNumbers = opts.IncludePageNumbers
	markdown, warnings, err := extractor.ToMarkdownWithOptions(markdownOpts)
	if err != nil {
		return Result{}, err
	}
	if len(markdown) > opts.MaxOutputBytes {
		return Result{}, ErrResourceLimit
	}
	if len(warnings) > 64 {
		warnings = warnings[:64]
	}
	for i := range warnings {
		if len(warnings[i].Message) > 4096 {
			warnings[i].Message = warnings[i].Message[:4096]
		}
	}
	return Result{Markdown: markdown, Warnings: warnings}, nil
}

func preflight(path string, opts Options) error {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".pdf" {
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		magic := make([]byte, 5)
		if _, err = io.ReadFull(file, magic); err != nil || !bytes.Equal(magic, []byte("%PDF-")) {
			return errors.New("document parser: invalid PDF signature")
		}
		return nil
	}
	switch ext {
	case ".docx", ".xlsx", ".pptx", ".odt", ".epub":
	default:
		return nil
	}
	archive, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("document parser: invalid ZIP container: %w", err)
	}
	defer archive.Close()
	if len(archive.File) > opts.MaxArchiveEntries {
		return fmt.Errorf("%w: too many archive entries", ErrResourceLimit)
	}
	var total uint64
	for _, file := range archive.File {
		if file.Mode()&os.ModeSymlink != 0 || strings.HasPrefix(file.Name, "/") || strings.Contains(file.Name, "\\") || strings.Contains(file.Name, "../") {
			return errors.New("document parser: invalid archive entry")
		}
		if file.UncompressedSize64 > opts.MaxExpandedBytes-total {
			return fmt.Errorf("%w: expanded archive too large", ErrResourceLimit)
		}
		total += file.UncompressedSize64
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

type boundedWriter struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	n := len(p)
	remaining := w.limit - w.Len()
	if len(p) > remaining {
		p = p[:remaining]
		w.exceeded = true
	}
	_, _ = w.Buffer.Write(p)
	return n, nil
}
