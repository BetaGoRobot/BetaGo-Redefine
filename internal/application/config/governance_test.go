package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNoDirectConfigGetInBusinessPaths(t *testing.T) {
	t.Helper()

	repoRoot, scanRoots := governanceScanRoots(t)
	allowlist := map[string]struct{}{
		filepath.ToSlash(filepath.Join("internal", "application", "lark", "botidentity", "identity.go")): {},
	}

	violations := scanBusinessFiles(t, repoRoot, scanRoots, allowlist, func(content string) bool {
		return strings.Contains(content, "config.Get(")
	})

	if len(violations) > 0 {
		t.Fatalf("found direct config.Get() in business paths: %s", strings.Join(violations, ", "))
	}
}

func TestNoScatteredLegacyUserFieldUsageInBusinessPaths(t *testing.T) {
	t.Helper()

	repoRoot, scanRoots := governanceScanRoots(t)
	allowlist := map[string]struct{}{
		filepath.ToSlash(filepath.Join("internal", "application", "lark", "botidentity", "open_id.go")): {},
		filepath.ToSlash(filepath.Join("internal", "application", "lark", "botidentity", "identity.go")):      {},
	}

	violations := scanBusinessFiles(t, repoRoot, scanRoots, allowlist, func(content string) bool {
		return strings.Contains(content, ".UserId")
	})

	if len(violations) > 0 {
		t.Fatalf("found scattered legacy UserId usage in business paths: %s", strings.Join(violations, ", "))
	}
}

func TestNoUnexpectedGoFuncInBusinessPaths(t *testing.T) {
	t.Helper()

	repoRoot, scanRoots := governanceScanRoots(t)
	allowlist := map[string]struct{}{
		filepath.ToSlash(filepath.Join("internal", "interfaces", "lark", "handler.go")):                      {},
		filepath.ToSlash(filepath.Join("internal", "application", "lark", "messages", "recording", "service.go")): {},
	}

	violations := scanBusinessFiles(t, repoRoot, scanRoots, allowlist, func(content string) bool {
		return strings.Contains(content, "go func(")
	})

	if len(violations) > 0 {
		t.Fatalf("found unexpected go func() in business paths: %s", strings.Join(violations, ", "))
	}
}

func governanceScanRoots(t *testing.T) (string, []string) {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve current file path")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", ".."))
	return repoRoot, []string{
		filepath.Join(repoRoot, "internal", "application", "lark"),
		filepath.Join(repoRoot, "internal", "interfaces", "lark"),
	}
}

func scanBusinessFiles(t *testing.T, repoRoot string, scanRoots []string, allowlist map[string]struct{}, predicate func(content string) bool) []string {
	t.Helper()

	var violations []string
	for _, root := range scanRoots {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}

			rel, err := filepath.Rel(repoRoot, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if _, ok := allowlist[rel]; ok {
				return nil
			}

			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if predicate(string(content)) {
				violations = append(violations, rel)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	return violations
}
