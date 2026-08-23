package repoapp

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile is a tiny fixture helper for repo layouts.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPlanStackNode(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{
	  "scripts": {"start": "node server.js"},
	  "packageManager": "pnpm@9.0.0",
	  "engines": {"node": ">=18"}
	}`)
	writeFile(t, dir, "pnpm-lock.yaml", "")
	plan, err := PlanStack(dir, "a-b")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Stack != StackNode {
		t.Errorf("stack = %q want node", plan.Stack)
	}
	if plan.PackageManager != "pnpm" {
		t.Errorf("pm = %q want pnpm", plan.PackageManager)
	}
	if plan.StartCmd != "pnpm run start" {
		t.Errorf("start = %q", plan.StartCmd)
	}
	if plan.NodeReq != ">=18" {
		t.Errorf("node req = %q", plan.NodeReq)
	}
}

func TestPlanStackNodeNoStartScript(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"scripts": {"build": "tsc"}}`)
	if _, err := PlanStack(dir, "a-b"); err == nil {
		t.Error("node project without start/dev script must error with a hint")
	}
}

func TestPlanStackPython(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "requirements.txt", "flask\n")
	writeFile(t, dir, "app.py", "print('hi')\n")
	writeFile(t, dir, ".python-version", "3.11.4")
	plan, err := PlanStack(dir, "a-b")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Stack != StackPython {
		t.Errorf("stack = %q want python", plan.Stack)
	}
	if plan.StartCmd != "python app.py" {
		t.Errorf("start = %q", plan.StartCmd)
	}
	if plan.PythonReq != "3.11.4" {
		t.Errorf("python req = %q", plan.PythonReq)
	}
}

func TestPlanStackPythonFromPyproject(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pyproject.toml", "[project]\nname = \"x\"\nrequires-python = \">=3.10\"\n")
	writeFile(t, dir, "main.py", "")
	plan, err := PlanStack(dir, "a-b")
	if err != nil {
		t.Fatal(err)
	}
	if plan.PythonReq != ">=3.10" {
		t.Errorf("python req = %q", plan.PythonReq)
	}
}

func TestPlanStackDocker(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Dockerfile", "FROM alpine\n")
	plan, err := PlanStack(dir, "owner-repo")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Stack != StackDocker {
		t.Errorf("stack = %q want docker", plan.Stack)
	}
	if want := "docker run --rm -p 127.0.0.1:${PORT}:${PORT} -e PORT repoapp-owner-repo"; plan.StartCmd != want {
		t.Errorf("start = %q want %q", plan.StartCmd, want)
	}
}

func TestPlanStackUnknown(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "nothing runnable\n")
	if _, err := PlanStack(dir, "a-b"); err == nil {
		t.Error("empty project must error as unsupported")
	}
}

func TestReadRequirementsNvmrc(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"engines": {"node": ">=18"}}`)
	writeFile(t, dir, ".nvmrc", "v20\n")
	r := ReadRequirements(dir)
	if r.Node != "20" {
		t.Errorf(".nvmrc should win over engines, got %q", r.Node)
	}
}

func TestVersionSatisfaction(t *testing.T) {
	cases := []struct {
		found, req string
		py, node   bool
	}{
		{"Python 3.11.2", ">=3.10", true, true},
		{"Python 3.9.0", ">=3.10", false, false},
		{"Python 3.11.2", ">=3.10,<3.12", true, true},
		{"Python 3.12.0", ">=3.10,<3.12", false, false},
		{"Python 3.11.2", "3.11", true, true},
		{"Python 3.11.2", "", true, true},
		{"v20.11.0", ">=18", true, true},
		{"v16.0.0", ">=18", false, false},
		{"v20.11.0", "20", true, true},
		{"v20.11.0", "^18", false, false},
	}
	for _, c := range cases {
		if got := pythonSatisfies(c.found, c.req); got != c.py {
			t.Errorf("pythonSatisfies(%q,%q) = %v want %v", c.found, c.req, got, c.py)
		}
		if got := nodeSatisfies(c.found, c.req); got != c.node {
			t.Errorf("nodeSatisfies(%q,%q) = %v want %v", c.found, c.req, got, c.node)
		}
	}
}

func TestInstallVersionDerivation(t *testing.T) {
	if got := pythonInstallVersion(">=3.10"); got != "3.10" {
		t.Errorf("pythonInstallVersion = %q", got)
	}
	if got := pythonInstallVersion(""); got != "" {
		t.Errorf("empty python req should yield uv default, got %q", got)
	}
	if got := nodeInstallVersion("^20"); got != "20" {
		t.Errorf("nodeInstallVersion = %q", got)
	}
	if got := nodeInstallVersion(""); got != "lts" {
		t.Errorf("empty node req should yield lts, got %q", got)
	}
	if got := nodeInstallVersion("v18.17.0"); got != "18" {
		t.Errorf("nodeInstallVersion = %q", got)
	}
}
