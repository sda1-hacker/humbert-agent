package builtin

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
)

const (
	globFilesToolName     = "glob_files"
	grepFilesToolName     = "grep_files"
	searchFilesMaxResults = 500
)

type GlobFilesInput struct {
	Path       string `json:"path,omitempty" jsonschema:"description=Directory to search. Relative paths are resolved from the current workspace."`
	Pattern    string `json:"pattern" jsonschema:"description=File name glob, for example *.go or *.vue."`
	MaxResults int    `json:"max_results,omitempty" jsonschema:"description=Maximum results to return. Defaults to 200 and is capped at 500."`
}

type GlobFilesOutput struct {
	Files     []string `json:"files"`
	Truncated bool     `json:"truncated"`
}

type GlobFilesFactory struct{}

func NewGlobFilesFactory() *GlobFilesFactory { return &GlobFilesFactory{} }
func (f *GlobFilesFactory) Descriptor() humberttools.Descriptor {
	return humberttools.Descriptor{Name: globFilesToolName, Risk: humberttools.RiskRead}
}
func (f *GlobFilesFactory) Build(ctx context.Context, scope humberttools.Scope) (einotool.InvokableTool, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return utils.InferTool(globFilesToolName,
		"Find files by file-name glob inside directories allowed by the current Agent Sandbox. Does not follow symbolic-link directories.",
		func(callCtx context.Context, input *GlobFilesInput) (*GlobFilesOutput, error) {
			if input == nil || strings.TrimSpace(input.Pattern) == "" {
				return nil, errors.New("glob_files pattern 不能为空")
			}
			if _, err := filepath.Match(input.Pattern, "probe"); err != nil {
				return nil, fmt.Errorf("glob_files pattern 无效: %w", err)
			}
			limit := input.MaxResults
			if limit <= 0 {
				limit = 200
			}
			if limit > searchFilesMaxResults {
				limit = searchFilesMaxResults
			}
			target, err := openSandboxTarget(callCtx, scope, defaultSearchPath(input.Path), sandbox.OpSearch)
			if err != nil {
				return nil, fmt.Errorf("glob_files 路径被 Sandbox 拒绝: %w", err)
			}
			defer target.Close()
			files := make([]string, 0, limit)
			truncated := false
			err = walkRoot(callCtx, target, func(rel string, entry fs.DirEntry) error {
				if entry.IsDir() {
					return nil
				}
				ok, _ := filepath.Match(input.Pattern, entry.Name())
				if !ok {
					return nil
				}
				display := displayRootChild(target, rel)
				files = append(files, display)
				if len(files) >= limit {
					truncated = true
					return errStopWalk
				}
				return nil
			})
			if err != nil && !errors.Is(err, errStopWalk) {
				return nil, err
			}
			sort.Strings(files)
			return &GlobFilesOutput{Files: files, Truncated: truncated}, nil
		},
	)
}

type GrepFilesInput struct {
	Pattern       string `json:"pattern" jsonschema:"description=Regular expression to search for."`
	Path          string `json:"path,omitempty" jsonschema:"description=Directory to search. Relative paths are resolved from the workspace."`
	FileGlob      string `json:"file_glob,omitempty" jsonschema:"description=Optional file-name glob such as *.go."`
	CaseSensitive bool   `json:"case_sensitive,omitempty" jsonschema:"description=Use case-sensitive matching. Defaults to false."`
	MaxResults    int    `json:"max_results,omitempty" jsonschema:"description=Maximum matching lines. Defaults to 200 and is capped at 500."`
}

type GrepMatch struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}
type GrepFilesOutput struct {
	Matches   []GrepMatch `json:"matches"`
	Truncated bool        `json:"truncated"`
}
type GrepFilesFactory struct{}

func NewGrepFilesFactory() *GrepFilesFactory { return &GrepFilesFactory{} }
func (f *GrepFilesFactory) Descriptor() humberttools.Descriptor {
	return humberttools.Descriptor{Name: grepFilesToolName, Risk: humberttools.RiskRead}
}
func (f *GrepFilesFactory) Build(ctx context.Context, scope humberttools.Scope) (einotool.InvokableTool, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return utils.InferTool(grepFilesToolName,
		"Search UTF-8/text files with a regular expression inside directories allowed by the Agent Sandbox. Does not follow symbolic-link directories.",
		func(callCtx context.Context, input *GrepFilesInput) (*GrepFilesOutput, error) {
			if input == nil || strings.TrimSpace(input.Pattern) == "" {
				return nil, errors.New("grep_files pattern 不能为空")
			}
			pattern := input.Pattern
			if !input.CaseSensitive {
				pattern = "(?i)" + pattern
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				return nil, fmt.Errorf("grep_files 正则无效: %w", err)
			}
			if input.FileGlob != "" {
				if _, err := filepath.Match(input.FileGlob, "probe"); err != nil {
					return nil, fmt.Errorf("file_glob 无效: %w", err)
				}
			}
			limit := input.MaxResults
			if limit <= 0 {
				limit = 200
			}
			if limit > searchFilesMaxResults {
				limit = searchFilesMaxResults
			}
			target, err := openSandboxTarget(callCtx, scope, defaultSearchPath(input.Path), sandbox.OpSearch)
			if err != nil {
				return nil, fmt.Errorf("grep_files 路径被 Sandbox 拒绝: %w", err)
			}
			defer target.Close()
			matches := make([]GrepMatch, 0, limit)
			truncated := false
			err = walkRoot(callCtx, target, func(rel string, entry fs.DirEntry) error {
				if entry.IsDir() {
					return nil
				}
				if input.FileGlob != "" {
					ok, _ := filepath.Match(input.FileGlob, entry.Name())
					if !ok {
						return nil
					}
				}
				f, openErr := target.root.Open(rel)
				if openErr != nil {
					return nil
				}
				defer f.Close()
				scanner := bufio.NewScanner(f)
				scanner.Buffer(make([]byte, 64*1024), 1024*1024)
				line := 0
				for scanner.Scan() {
					if err := callCtx.Err(); err != nil {
						return err
					}
					line++
					text := scanner.Text()
					if re.MatchString(text) {
						if len(text) > 2000 {
							text = text[:2000] + "…"
						}
						matches = append(matches, GrepMatch{Path: displayRootChild(target, rel), Line: line, Text: text})
						if len(matches) >= limit {
							truncated = true
							return errStopWalk
						}
					}
				}
				if scanErr := scanner.Err(); scanErr != nil {
					// 单个超长行/不可读文件不应让整个搜索失败；该文件直接跳过。
					return nil
				}
				return nil
			})
			if err != nil && !errors.Is(err, errStopWalk) {
				return nil, err
			}
			return &GrepFilesOutput{Matches: matches, Truncated: truncated}, nil
		},
	)
}

var errStopWalk = errors.New("stop walk")

func defaultSearchPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return "."
	}
	return path
}

func displayRootChild(target *sandboxTarget, rel string) string {
	if target.display == "." {
		return filepath.ToSlash(rel)
	}
	if filepath.IsAbs(target.display) {
		return filepath.Join(target.display, rel)
	}
	return filepath.ToSlash(filepath.Join(target.display, rel))
}

func walkRoot(ctx context.Context, target *sandboxTarget, visit func(rel string, entry fs.DirEntry) error) error {
	start := target.relative
	if start == "." {
		start = "."
	}
	var walk func(string) error
	walk = func(rel string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		f, err := target.root.Open(rel)
		if err != nil {
			return nil
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return nil
		}
		if !info.IsDir() {
			f.Close()
			entry := fs.FileInfoToDirEntry(info)
			return visit(rel, entry)
		}
		entries, err := f.ReadDir(-1)
		f.Close()
		if err != nil {
			return nil
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			child := entry.Name()
			if rel != "." && rel != "" {
				child = filepath.Join(rel, entry.Name())
			}
			if entry.Name() == ".git" && entry.IsDir() {
				continue
			}
			if entry.Type()&fs.ModeSymlink != 0 {
				continue
			}
			if err := visit(child, entry); err != nil {
				return err
			}
			if entry.IsDir() {
				if err := walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(start)
}
