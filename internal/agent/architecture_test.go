package agent_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const projectModule = "github.com/BetaGoRobot/BetaGo-Redefine"

func allowedProjectImport(path string) bool {
	for _, root := range []string{"github.com/opensearch-project", "github.com/olivere/elastic", "github.com/elastic/go-elasticsearch"} {
		if path == root || strings.HasPrefix(path, root+"/") {
			return false
		}
	}
	if path != projectModule && !strings.HasPrefix(path, projectModule+"/") {
		return true
	}
	return path == projectModule+"/cmd/lark-agent" || path == projectModule+"/internal/agent" || strings.HasPrefix(path, projectModule+"/internal/agent/") || path == projectModule+"/internal/infrastructure/mcpclient" || path == projectModule+"/pkg/larkemoji"
}

func TestArchitectureAllowlistRejectsAllOldModuleRoots(t *testing.T) {
	for _, path := range []string{"context", "github.com/cloudwego/eino/compose", projectModule + "/internal/agent/domain", projectModule + "/internal/infrastructure/mcpclient", projectModule + "/pkg/larkemoji"} {
		if !allowedProjectImport(path) {
			t.Errorf("rejected atomic dependency %s", path)
		}
	}
	for _, root := range []string{"internal/application/lark/messages", "internal/application/lark/handlers", "internal/application/lark/agentruntime", "internal/application/lark/agentnext", "internal/application/lark/history", "internal/infrastructure/agentstore", "internal/infrastructure/einoagent", "internal/infrastructure/larkmedia", "internal/infrastructure/retriever", "internal/infrastructure/opensearch", "internal/application/config", "cmd/larkrobot", "internal/infrastructure/mcpclient/extra"} {
		if allowedProjectImport(projectModule + "/" + root) {
			t.Errorf("allowed old orchestration root %s", root)
		}
	}
	for _, path := range []string{
		projectModule + "/pkg/larkemoji/extra",
		projectModule + "/pkg/utils",
		"github.com/opensearch-project/opensearch-go",
		"github.com/opensearch-project/opensearch-go/v4/opensearchapi",
		"github.com/olivere/elastic",
		"github.com/olivere/elastic/v7",
		"github.com/elastic/go-elasticsearch",
		"github.com/elastic/go-elasticsearch/v8",
		"github.com/elastic/go-elasticsearch/v8/esapi",
	} {
		if allowedProjectImport(path) {
			t.Errorf("allowed old search client %s", path)
		}
	}
}

func TestNewPackagesImportOnlyNewAgentOrApprovedAtomicLeaf(t *testing.T) {
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			pathValue, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			if !allowedProjectImport(pathValue) {
				t.Errorf("%s imports forbidden orchestration: %s", path, pathValue)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNewCommandTransitiveDependenciesExcludeOldOrchestration(t *testing.T) {
	root := filepath.Join("..", "..")
	command := filepath.Join(root, "cmd", "lark-agent")
	if _, err := os.Stat(command); os.IsNotExist(err) {
		t.Skip("phase limitation: cmd/lark-agent is not implemented yet; transitive command guard will run when added")
	} else if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "list", "-tags=custom_skip_vips", "-deps", "-f", "{{.ImportPath}}", "./cmd/lark-agent")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("list actual command dependencies: %v\n%s", err, out)
	}
	for _, path := range strings.Fields(string(out)) {
		if !allowedProjectImport(path) {
			t.Errorf("new command transitively imports forbidden module package: %s", path)
		}
	}
}
