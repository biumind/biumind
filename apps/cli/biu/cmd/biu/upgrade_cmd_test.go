package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/install"
	"github.com/biumind/biumind/apps/cli/biu/internal/selfupdate"
	"github.com/biumind/biumind/apps/cli/biu/internal/updatecheck"
)

// upgradeFixture swaps every injection point with a mock and restores
// them (plus the version build var) on cleanup.
type upgradeFixture struct {
	t           *testing.T
	detect      install.Method
	hint        string
	checkRes    *updatecheck.Result
	checkErr    error
	checkOpt    updatecheck.Options
	applyOpt    selfupdate.Options
	applyErr    error
	delegated   string
	delegateErr error
}

func newUpgradeFixture(t *testing.T) *upgradeFixture {
	t.Helper()
	fx := &upgradeFixture{t: t, detect: install.MethodUnknown}

	origCheck, origApply := upgradeCheckFn, upgradeApplyFn
	origDetect, origDelegate := upgradeDetectFn, upgradeDelegateFn
	origVersion := version
	t.Cleanup(func() {
		upgradeCheckFn, upgradeApplyFn = origCheck, origApply
		upgradeDetectFn, upgradeDelegateFn = origDetect, origDelegate
		version = origVersion
	})

	version = "0.1.0"
	upgradeDetectFn = func(string) (install.Method, string) { return fx.detect, fx.hint }
	upgradeCheckFn = func(_ context.Context, opt updatecheck.Options) (*updatecheck.Result, error) {
		fx.checkOpt = opt
		return fx.checkRes, fx.checkErr
	}
	upgradeApplyFn = func(_ context.Context, opt selfupdate.Options) error {
		fx.applyOpt = opt
		return fx.applyErr
	}
	upgradeDelegateFn = func(_ context.Context, cmdline string) (string, error) {
		fx.delegated = cmdline
		return "delegated output", fx.delegateErr
	}
	return fx
}

func (fx *upgradeFixture) run(args ...string) (stdout, stderr string, err error) {
	cmd := newUpgradeCmd(&rootFlags{})
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestUpgrade_skipBuildRefuses(t *testing.T) {
	fx := newUpgradeFixture(t)
	version = "0.1.0-dev"
	_, _, err := fx.run()
	if err == nil || !strings.Contains(err.Error(), "not available for this build") {
		t.Errorf("dev build should refuse, got %v", err)
	}
}

func TestUpgrade_clientManagedRefuses(t *testing.T) {
	fx := newUpgradeFixture(t)
	fx.detect = install.MethodClientManaged
	_, _, err := fx.run()
	if err == nil || !strings.Contains(err.Error(), "desktop client") {
		t.Errorf("client-managed should refuse, got %v", err)
	}
}

func TestUpgrade_delegateRunsHint(t *testing.T) {
	fx := newUpgradeFixture(t)
	fx.detect, fx.hint = install.MethodHomebrew, "brew upgrade biumind/tap/biu"
	stdout, stderr, err := fx.run()
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if fx.delegated != "brew upgrade biumind/tap/biu" {
		t.Errorf("hint not delegated, got %q", fx.delegated)
	}
	if !strings.Contains(stdout, "delegated output") {
		t.Errorf("command output should go to stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "restart biu") {
		t.Errorf("restart note should go to stderr: %q", stderr)
	}
}

func TestUpgrade_delegateFailureSurfaces(t *testing.T) {
	fx := newUpgradeFixture(t)
	fx.detect, fx.hint = install.MethodSnap, "sudo snap refresh biu"
	fx.delegateErr = errors.New("snapd offline")
	_, _, err := fx.run()
	if err == nil || !strings.Contains(err.Error(), "snapd offline") {
		t.Errorf("delegate failure should surface, got %v", err)
	}
}

func TestUpgrade_alreadyUpToDate(t *testing.T) {
	fx := newUpgradeFixture(t)
	fx.checkRes = &updatecheck.Result{Current: "0.1.0", Latest: "0.1.0", Source: "github"}
	stdout, _, err := fx.run()
	if err != nil {
		t.Fatalf("up-to-date should exit 0: %v", err)
	}
	if !strings.Contains(stdout, "already up to date") {
		t.Errorf("missing up-to-date note: %q", stdout)
	}
}

func TestUpgrade_selfUpdateSuccess(t *testing.T) {
	fx := newUpgradeFixture(t)
	fx.checkRes = &updatecheck.Result{
		Current: "0.1.0", Latest: "0.2.0", Newer: true, Source: "github",
		AssetURL: "https://example.com/biu.tar.gz", AssetFilename: "biu.tar.gz",
		AssetSha256: "deadbeef",
	}
	stdout, _, err := fx.run()
	if err != nil {
		t.Fatalf("self-update: %v", err)
	}
	if fx.applyOpt.AssetURL != "https://example.com/biu.tar.gz" || fx.applyOpt.AssetSha256 != "deadbeef" {
		t.Errorf("apply options not threaded: %+v", fx.applyOpt)
	}
	if !strings.Contains(stdout, "biu 0.2.0 installed (was 0.1.0)") {
		t.Errorf("success note missing: %q", stdout)
	}
}

func TestUpgrade_noAssetErrors(t *testing.T) {
	fx := newUpgradeFixture(t)
	fx.checkRes = &updatecheck.Result{
		Current: "0.1.0", Latest: "0.2.0", Newer: true, Source: "github",
		ReleaseURL: "https://github.com/biumind/biumind/releases/tag/v0.2.0",
	}
	_, _, err := fx.run()
	if err == nil || !strings.Contains(err.Error(), "no downloadable asset") {
		t.Fatalf("expected no-asset error, got %v", err)
	}
	if !strings.Contains(err.Error(), "releases/tag/v0.2.0") {
		t.Errorf("hint should link the release page: %v", err)
	}
}

func TestUpgrade_applyFailurePassthrough(t *testing.T) {
	fx := newUpgradeFixture(t)
	fx.checkRes = &updatecheck.Result{
		Current: "0.1.0", Latest: "0.2.0", Newer: true, Source: "github",
		AssetURL: "https://example.com/biu.tar.gz", AssetSha256: "deadbeef",
	}
	fx.applyErr = errors.New("sha256 mismatch")
	_, _, err := fx.run()
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Errorf("apply failure should surface, got %v", err)
	}
}

func TestUpgrade_checkErrorPassthrough(t *testing.T) {
	fx := newUpgradeFixture(t)
	fx.checkErr = errors.New("github: 403 forbidden")
	_, _, err := fx.run()
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("check failure should surface, got %v", err)
	}
}

func TestUpgradeCheck_newerOutput(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // RecordCheck writes ~/.biu
	fx := newUpgradeFixture(t)
	fx.checkRes = &updatecheck.Result{
		Current: "0.1.0", Latest: "0.2.0", Newer: true, Source: "github",
		DownloadSource: "oss",
		ReleaseURL:     "https://github.com/biumind/biumind/releases/tag/v0.2.0",
	}
	stdout, _, err := fx.run("check")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, want := range []string{"biu 0.2.0 available (current 0.1.0)", "source:  github", "download via: oss", "release:", "biu upgrade"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("missing %q: %s", want, stdout)
		}
	}
	// The explicit check refreshes the state cache for `skip`.
	state, err := updatecheck.LoadState("")
	if err != nil {
		t.Fatal(err)
	}
	if state.LatestVersion != "0.2.0" {
		t.Errorf("latest not recorded: %+v", state)
	}
}

func TestUpgradeCheck_upToDate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fx := newUpgradeFixture(t)
	fx.checkRes = &updatecheck.Result{Current: "0.1.0", Latest: "0.1.0", Source: "github"}
	stdout, _, err := fx.run("check")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !strings.Contains(stdout, "up to date (biu 0.1.0, github)") {
		t.Errorf("missing up-to-date note: %q", stdout)
	}
}

func TestUpgradeCheck_skipBuildRefuses(t *testing.T) {
	fx := newUpgradeFixture(t)
	version = "1.2.3+clientmanaged"
	_, _, err := fx.run("check")
	if err == nil || !strings.Contains(err.Error(), "skipped for this build") {
		t.Errorf("client-managed build should skip checks, got %v", err)
	}
}

func TestUpgradeSkip_explicitVersion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, stderr, err := newUpgradeFixtureRun("skip", "0.4.1")
	if err != nil {
		t.Fatalf("skip: %v", err)
	}
	if !strings.Contains(stderr, "0.4.1") {
		t.Errorf("confirmation should echo the version: %q", stderr)
	}
	state, err := updatecheck.LoadState("")
	if err != nil {
		t.Fatal(err)
	}
	if state.SkippedVersion != "0.4.1" {
		t.Errorf("skipped version not persisted: %+v", state)
	}
}

func TestUpgradeSkip_bareNeedsKnownVersion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, _, err := newUpgradeFixtureRun("skip")
	if err == nil || !strings.Contains(err.Error(), "nothing to skip") {
		t.Errorf("empty state should error with guidance, got %v", err)
	}

	if err := updatecheck.RecordCheck("0.5.0"); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := newUpgradeFixtureRun("skip")
	if err != nil {
		t.Fatalf("bare skip: %v", err)
	}
	if !strings.Contains(stderr, "0.5.0") {
		t.Errorf("bare skip should use latest known version: %q", stderr)
	}
}

// newUpgradeFixtureRun runs the skip subcommand, which touches none of
// the check/apply injection points.
func newUpgradeFixtureRun(args ...string) (stdout, stderr string, err error) {
	cmd := newUpgradeCmd(&rootFlags{})
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestUpgrade_registeredWithAlias(t *testing.T) {
	root := newRootCmd()
	cmd, _, err := root.Find([]string{"update"})
	if err != nil || cmd == nil || cmd.Name() != "upgrade" {
		t.Fatalf("alias update should resolve to upgrade: cmd=%v err=%v", cmd, err)
	}
	if cmd.Name() != "upgrade" {
		t.Errorf("resolved command = %q", cmd.Name())
	}
}
