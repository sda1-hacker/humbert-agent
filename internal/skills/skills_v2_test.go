package skills

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sda1-hacker/humbert-agent/internal/config"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
)

func TestInspectPackageSkillV2MetadataAndCompatibility(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSkillTestFile(t, root, "SKILL.md", `---
name: standard-skill
description: A standard skill used by Skills V2 tests.
license: MIT
compatibility: Requires Python when optional scripts are used.
metadata:
  owner: humbert
  version: 2
allowed-tools:
  - read_file
  - web_search
---
Use the available tools carefully.
`)
	writeSkillTestFile(t, root, "scripts/example.ts", `console.log("test")`)

	pkg, err := inspectPackage(context.Background(), root, testSkillConfig(), false)
	if err != nil {
		t.Fatalf("inspectPackage() error = %v", err)
	}
	if pkg.Info.SpecStatus != SpecStatusStandard {
		t.Fatalf("SpecStatus = %q, want %q (%s)", pkg.Info.SpecStatus, SpecStatusStandard, pkg.Info.SpecMessage)
	}
	if pkg.Info.License != "MIT" {
		t.Fatalf("License = %q", pkg.Info.License)
	}
	if pkg.Info.Metadata["version"] != "2" {
		t.Fatalf("metadata.version = %q", pkg.Info.Metadata["version"])
	}
	if pkg.Info.AllowedTools != "read_file web_search" {
		t.Fatalf("AllowedTools = %q", pkg.Info.AllowedTools)
	}
	if pkg.Info.RuntimeStatus != RuntimeStatusNeedsSetup {
		t.Fatalf("RuntimeStatus = %q, want %q", pkg.Info.RuntimeStatus, RuntimeStatusNeedsSetup)
	}
	if len(pkg.Info.ScriptRuntimes) != 1 || pkg.Info.ScriptRuntimes[0].Path != "scripts/example.ts" {
		t.Fatalf("ScriptRuntimes = %#v", pkg.Info.ScriptRuntimes)
	}
}

func TestInspectPackageEinoExtensionInstallsButCannotActivate(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSkillTestFile(t, root, "SKILL.md", `---
name: fork-skill
description: This package declares an Eino runtime extension.
context: fork
---
Do the delegated work.
`)

	pkg, err := inspectPackage(context.Background(), root, testSkillConfig(), false)
	if err != nil {
		t.Fatalf("inspectPackage() should allow installation metadata, error = %v", err)
	}
	if !pkg.Info.Valid {
		t.Fatal("package should remain valid for installation")
	}
	if pkg.Info.RuntimeStatus != RuntimeStatusUnsupported {
		t.Fatalf("RuntimeStatus = %q, want %q", pkg.Info.RuntimeStatus, RuntimeStatusUnsupported)
	}
}

func TestDiscoverRepositoryKeepsInvalidCandidateAndInstallsSelection(t *testing.T) {
	t.Parallel()
	catalogRoot := t.TempDir()
	sourceRoot := t.TempDir()
	writeSkillTestFile(t, sourceRoot, "skills/good-skill/SKILL.md", `---
name: good-skill
description: A valid discovered skill.
---
Use this skill.
`)
	writeSkillTestFile(t, sourceRoot, "skills/bad-skill/SKILL.md", `---
name: BAD SKILL
description: Invalid name.
---
This candidate should not block discovery.
`)

	manager, err := NewManager(context.Background(), catalogRoot, testSkillConfig(), logging.NewBootstrap())
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	discovery, err := manager.DiscoverFromDirectory(context.Background(), sourceRoot)
	if err != nil {
		t.Fatalf("DiscoverFromDirectory() error = %v", err)
	}
	if len(discovery.Candidates) != 2 {
		t.Fatalf("candidate count = %d, want 2: %#v", len(discovery.Candidates), discovery.Candidates)
	}
	valid := 0
	invalid := 0
	for _, candidate := range discovery.Candidates {
		if candidate.Info.Valid {
			valid++
		} else {
			invalid++
		}
	}
	if valid != 1 || invalid != 1 {
		t.Fatalf("valid=%d invalid=%d, want 1/1", valid, invalid)
	}

	installed, err := manager.InstallDiscoveredFromDirectory(
		context.Background(),
		sourceRoot,
		[]string{"skills/good-skill"},
	)
	if err != nil {
		t.Fatalf("InstallDiscoveredFromDirectory() error = %v", err)
	}
	if len(installed) != 1 || installed[0].Name != "good-skill" {
		t.Fatalf("installed = %#v", installed)
	}
	if _, err := os.Stat(filepath.Join(catalogRoot, "good-skill", "SKILL.md")); err != nil {
		t.Fatalf("installed SKILL.md missing: %v", err)
	}

	identities, revision, err := manager.RuntimeIdentities(context.Background(), []string{"good-skill"})
	if err != nil {
		t.Fatalf("RuntimeIdentities() error = %v", err)
	}
	if identities["good-skill"] == "" || revision == "" {
		t.Fatalf("identities=%#v revision=%q", identities, revision)
	}
}

func TestDiscoverRepositoryAllInvalidStillReturnsDiagnostics(t *testing.T) {
	t.Parallel()
	catalogRoot := t.TempDir()
	sourceRoot := t.TempDir()
	writeSkillTestFile(t, sourceRoot, "skills/bad/SKILL.md", `---
name: INVALID NAME
description: Invalid package.
---
Body.
`)

	manager, err := NewManager(context.Background(), catalogRoot, testSkillConfig(), logging.NewBootstrap())
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	discovery, err := manager.DiscoverFromDirectory(context.Background(), sourceRoot)
	if err != nil {
		t.Fatalf("DiscoverFromDirectory() error = %v", err)
	}
	if len(discovery.Candidates) != 1 || discovery.Candidates[0].Info.Valid {
		t.Fatalf("discovery = %#v", discovery.Candidates)
	}
}

func writeSkillTestFile(t *testing.T, root string, relative string, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatalf("MkdirAll(%s): %v", relative, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", relative, err)
	}
}

func testSkillConfig() config.SkillConfig {
	return config.SkillConfig{
		MaxDefinitionBytes: 512 * 1024,
		MaxAssetBytes:      2 * 1024 * 1024,
		MaxPackageBytes:    16 * 1024 * 1024,
		MaxFiles:           512,
		MaxDownloadBytes:   32 * 1024 * 1024,
		DownloadTimeoutMS:  30_000,
		MaxRedirects:       4,
	}
}

func TestGitHubTreeResolverKeepsExplicitSkillSubpath(t *testing.T) {
	t.Parallel()
	registry, err := NewDefaultRemoteSkillSourceRegistry()
	if err != nil {
		t.Fatalf("NewDefaultRemoteSkillSourceRegistry() error = %v", err)
	}
	resolved, err := registry.Resolve("https://github.com/tw93/Kami/tree/main/skills/kami", "")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Provider != "github" {
		t.Fatalf("Provider = %q, want github", resolved.Provider)
	}
	if resolved.SkillPath != "skills/kami" {
		t.Fatalf("SkillPath = %q, want skills/kami", resolved.SkillPath)
	}
	if resolved.Metadata["repository_owner"] != "tw93" || resolved.Metadata["repository_name"] != "Kami" {
		t.Fatalf("repository metadata = %#v", resolved.Metadata)
	}
	if resolved.Metadata["ref"] != "main" {
		t.Fatalf("ref = %q, want main", resolved.Metadata["ref"])
	}
	if resolved.DownloadURL == nil || resolved.DownloadURL.Host != "codeload.github.com" {
		t.Fatalf("explicit GitHub tree fallback should use codeload, got %#v", resolved.DownloadURL)
	}

	spec, ok := gitSkillRepositorySpecForSource(resolved)
	if !ok {
		t.Fatal("GitHub repository source should be handled by Git-first installer")
	}
	if spec.RemoteURL != "https://github.com/tw93/Kami.git" || spec.Ref != "main" || spec.SkillPath != "skills/kami" {
		t.Fatalf("git spec = %#v", spec)
	}
}

func TestGitSkillCandidateDirectoriesUsesTreeOnly(t *testing.T) {
	t.Parallel()
	tree := []byte("README.md\x00skills/kami/SKILL.md\x00skills/kami/assets/template.html\x00skills/other/SKILL.md\x00")
	got := gitSkillCandidateDirectories(tree)
	want := []string{"skills/kami", "skills/other"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("gitSkillCandidateDirectories() = %#v, want %#v", got, want)
	}
}

func TestSelectGitSkillArchivePathsPrefersExplicitPath(t *testing.T) {
	t.Parallel()
	candidates := []string{"skills/kami", "skills/other", "skills/other/nested"}
	got, err := selectGitSkillArchivePaths(candidates, "skills/kami", "")
	if err != nil {
		t.Fatalf("selectGitSkillArchivePaths() error = %v", err)
	}
	want := []string{"skills/kami"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %#v, want %#v", got, want)
	}
}

func TestSelectGitSkillArchivePathsBySkillName(t *testing.T) {
	t.Parallel()
	candidates := []string{"skills/frontend-design", "skills/pdf"}
	got, err := selectGitSkillArchivePaths(candidates, "", "frontend-design")
	if err != nil {
		t.Fatalf("selectGitSkillArchivePaths() error = %v", err)
	}
	want := []string{"skills/frontend-design"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %#v, want %#v", got, want)
	}
}

func TestMinimalGitSkillArchivePathsRootSkillMeansWholePackage(t *testing.T) {
	t.Parallel()
	if got := minimalGitSkillArchivePaths([]string{".", "skills/nested"}); got != nil {
		t.Fatalf("root SKILL.md should archive repository root, got %#v", got)
	}
}

func TestMinimalGitSkillArchivePathsDropsNestedDuplicate(t *testing.T) {
	t.Parallel()
	got := minimalGitSkillArchivePaths([]string{"skills/a/nested", "skills/a", "skills/b"})
	want := []string{"skills/a", "skills/b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %#v, want %#v", got, want)
	}
}

func TestGitExecutableCandidatesCoverDesktopMacInstallLocations(t *testing.T) {
	t.Parallel()
	got := gitExecutableCandidates("darwin", "", "")
	want := []string{"/opt/homebrew/bin/git", "/usr/local/bin/git", "/usr/bin/git"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("gitExecutableCandidates(darwin) = %#v, want %#v", got, want)
	}
}

func TestResolveSystemGitExecutableFindsCurrentTestGit(t *testing.T) {
	resolved, err := resolveSystemGitExecutable()
	if err != nil {
		t.Fatalf("resolveSystemGitExecutable() error = %v", err)
	}
	if filepath.Base(resolved) != "git" && filepath.Base(resolved) != "git.exe" {
		t.Fatalf("resolved git = %q", resolved)
	}
}
