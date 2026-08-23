package repoapp

import (
	"runtime"
	"strings"
	"testing"
)

// probesMap is a fixture builder for DecideBootstrap tests.
func probesMap(entries ...Probe) map[string]Probe {
	m := map[string]Probe{}
	for _, p := range entries {
		m[p.Name] = p
	}
	return m
}

func missing(name string) Probe { return Probe{Name: name, Source: SourceMissing} }
func system(name, version string) Probe {
	return Probe{Name: name, Source: SourceSystem, Path: "/usr/bin/" + name, Version: version}
}

func TestDecideBootstrapSystemPythonSatisfies(t *testing.T) {
	plan := DecideBootstrap(
		StackPlan{Stack: StackPython, PythonReq: ">=3.10"},
		probesMap(system("python3", "Python 3.11.2")),
	)
	if plan.DownloadUV || plan.InstallPython || plan.DownloadMise || plan.DockerMissing {
		t.Errorf("satisfying system python should need no bootstrap: %+v", plan)
	}
}

func TestDecideBootstrapMissingPythonDownloadsUV(t *testing.T) {
	plan := DecideBootstrap(
		StackPlan{Stack: StackPython, PythonReq: ">=3.10"},
		probesMap(), // nothing present
	)
	if !plan.DownloadUV || !plan.InstallPython {
		t.Errorf("missing python3+uv should bootstrap uv: %+v", plan)
	}
	if plan.PythonInstall != "3.10" {
		t.Errorf("PythonInstall = %q want 3.10", plan.PythonInstall)
	}
}

func TestDecideBootstrapUnsuitablePythonKeepsExistingUV(t *testing.T) {
	plan := DecideBootstrap(
		StackPlan{Stack: StackPython, PythonReq: ">=3.10"},
		probesMap(system("python3", "Python 3.9.0"), system("uv", "uv 0.5.0")),
	)
	if plan.DownloadUV {
		t.Error("uv already present — must not re-download")
	}
	if !plan.InstallPython {
		t.Error("unsuitable system python should trigger uv python install")
	}
}

func TestDecideBootstrapNodeViaMise(t *testing.T) {
	plan := DecideBootstrap(
		StackPlan{Stack: StackNode, NodeReq: "^20"},
		probesMap(system("node", "v16.0.0")), // too old, no mise
	)
	if !plan.DownloadMise || plan.NodeInstall != "20" {
		t.Errorf("old node + missing mise should bootstrap mise+node@20: %+v", plan)
	}
}

func TestDecideBootstrapNodeSatisfied(t *testing.T) {
	plan := DecideBootstrap(
		StackPlan{Stack: StackNode, NodeReq: ">=18"},
		probesMap(system("node", "v20.11.0")),
	)
	if plan.DownloadMise || plan.NodeInstall != "" {
		t.Errorf("satisfying system node should need no bootstrap: %+v", plan)
	}
}

func TestDecideBootstrapDocker(t *testing.T) {
	plan := DecideBootstrap(StackPlan{Stack: StackDocker}, probesMap())
	if !plan.DockerMissing {
		t.Error("docker stack without docker binary must flag DockerMissing")
	}
	plan = DecideBootstrap(StackPlan{Stack: StackDocker}, probesMap(system("docker", "Docker version 27")))
	if plan.DockerMissing {
		t.Error("docker present — must not flag DockerMissing")
	}
}

func TestAssetNames(t *testing.T) {
	// On a supported dev platform both assets must resolve; the strings
	// pin the upstream naming scheme so a silent upstream rename fails
	// loudly here rather than 404ing on users.
	uv, uvErr := uvAsset()
	mise, miseErr := miseAsset()
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64", "darwin/amd64", "linux/amd64", "linux/arm64":
		if uvErr != nil || miseErr != nil {
			t.Fatalf("supported platform must resolve assets: %v %v", uvErr, miseErr)
		}
		if !strings.HasPrefix(uv, "uv-") || !strings.HasSuffix(uv, ".tar.gz") {
			t.Errorf("uv asset shape wrong: %q", uv)
		}
		if !strings.HasPrefix(mise, "mise-latest-") {
			t.Errorf("mise asset shape wrong: %q", mise)
		}
	default:
		if uvErr == nil || miseErr == nil {
			t.Error("unsupported platform must not resolve assets")
		}
	}
}

func TestEnvWithPathExtra(t *testing.T) {
	env := envWithPathExtra([]string{"PATH=/usr/bin", "HOME=/x"}, []string{"/a/bin", "/b/bin"})
	want := "PATH=/a/bin:/b/bin:/usr/bin"
	if env[0] != want {
		t.Errorf("env[0] = %q want %q", env[0], want)
	}
	// No PATH in base env → append.
	env = envWithPathExtra([]string{"HOME=/x"}, []string{"/a/bin"})
	if env[len(env)-1] != "PATH=/a/bin" {
		t.Errorf("env = %v", env)
	}
	// Empty extra → unchanged.
	base := []string{"PATH=/usr/bin"}
	env = envWithPathExtra(base, nil)
	if env[0] != "PATH=/usr/bin" {
		t.Errorf("env = %v", env)
	}
}
