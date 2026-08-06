package repl

import (
	"strings"
	"testing"
)

func TestHandleUpgrade_bareShowsCommand(t *testing.T) {
	got := model{}.handleUpgrade([]string{"/upgrade"})
	for _, want := range []string{"biu ", "install method:"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q: %s", want, got)
		}
	}
}

func TestHandleUpgrade_check(t *testing.T) {
	got := model{}.handleUpgrade([]string{"/upgrade", "check"})
	// `check` should not include the "run /upgrade run" hint or
	// any "running…" output.
	if strings.Contains(got, "running…") {
		t.Errorf("check should not run: %s", got)
	}
}

func TestRunUpgradeCommand_rejectsShellMetachars(t *testing.T) {
	cases := []string{
		"echo hi; rm -rf /",
		"brew upgrade biu | tee /tmp/x",
		"go install foo > /dev/null",
		"`echo bad`",
	}
	for _, c := range cases {
		_, err := runUpgradeCommand(c)
		if err == nil || !strings.Contains(err.Error(), "metachars") {
			t.Errorf("metachar %q should be rejected, got err=%v", c, err)
		}
	}
}

func TestRunUpgradeCommand_emptyErrors(t *testing.T) {
	_, err := runUpgradeCommand("   ")
	if err == nil {
		t.Error("empty command should error")
	}
}
