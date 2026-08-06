// Pattern coverage tests. Each entry asserts a single command line
// produces the expected warning text, OR that an innocuous command
// produces "". A regression here surfaces as a visible failure
// rather than a silent drift.

package bashsec

import (
	"strings"
	"testing"
)

func TestWarningGitDestructive(t *testing.T) {
	cases := map[string]string{
		"git reset --hard":                       "may discard uncommitted changes",
		"git reset --hard HEAD~3":                "may discard uncommitted changes",
		"git push --force":                       "may overwrite remote history",
		"git push origin main --force-with-lease": "may overwrite remote history",
		"git push -f origin main":                "may overwrite remote history",
		"git clean -fd":                          "may permanently delete untracked files",
		"git checkout .":                         "may discard all working tree changes",
		"git checkout -- .":                      "may discard all working tree changes",
		"git restore .":                          "may discard all working tree changes",
		"git stash drop":                         "may permanently remove stashed changes",
		"git stash clear":                        "may permanently remove stashed changes",
		"git branch -D feature/x":                "may force-delete a branch",
		"git commit --no-verify -m 'fix'":        "may skip safety hooks",
		"git push --no-verify":                   "may skip safety hooks",
		"git commit --amend":                     "may rewrite the last commit",
	}
	for cmd, want := range cases {
		got := Warning(cmd)
		if got != want {
			t.Errorf("Warning(%q) = %q, want %q", cmd, got, want)
		}
	}
}

func TestWarningRm(t *testing.T) {
	cases := map[string]string{
		"rm -rf node_modules":      "may recursively force-remove files",
		"rm -fr build/":            "may recursively force-remove files",
		"rm -rf /tmp/x; rm -rf /tmp/y": "may recursively force-remove files",
		"rm -r oldproject":         "may recursively remove files",
		"rm -R legacy":             "may recursively remove files",
		"rm -f stale.lock":         "may force-remove files",
	}
	for cmd, want := range cases {
		if got := Warning(cmd); got != want {
			t.Errorf("Warning(%q) = %q, want %q", cmd, got, want)
		}
	}
}

func TestWarningDatabase(t *testing.T) {
	cases := map[string]string{
		"psql -c 'DROP TABLE users'":       "may drop or truncate database objects",
		"mysql -e 'TRUNCATE TABLE orders'": "may drop or truncate database objects",
		"psql -c 'drop database staging'":  "may drop or truncate database objects",
		// Lowercased schema also matches via case-insensitive flag.
		"clickhouse-client -q 'DROP SCHEMA analytics'": "may drop or truncate database objects",
		"psql -c 'DELETE FROM users;'":     "may delete all rows from a database table",
		"psql -c 'delete from sessions;'":  "may delete all rows from a database table",
	}
	for cmd, want := range cases {
		if got := Warning(cmd); got != want {
			t.Errorf("Warning(%q) = %q, want %q", cmd, got, want)
		}
	}
}

func TestWarningInfra(t *testing.T) {
	cases := map[string]string{
		"kubectl delete pod nginx":         "may delete Kubernetes resources",
		"kubectl delete -f deployment.yml": "may delete Kubernetes resources",
		"terraform destroy":                "may destroy Terraform infrastructure",
		"terraform destroy -auto-approve":  "may destroy Terraform infrastructure",
	}
	for cmd, want := range cases {
		if got := Warning(cmd); got != want {
			t.Errorf("Warning(%q) = %q, want %q", cmd, got, want)
		}
	}
}

// Innocuous commands must NOT trigger any warning — false positives
// would train users to dismiss the dialog.
func TestWarningEmptyForSafeCommands(t *testing.T) {
	safe := []string{
		"ls -la",
		"cat README.md",
		"git status",
		"git log --oneline",
		"git diff",
		"git pull",
		"git push", // bare push is fine; only --force etc. flagged
		"git commit -m 'add feature'",
		"echo hello",
		"npm install",
		"go test ./...",
		"rm somefile.txt", // no -r / -f flags
		"SELECT * FROM users",
		"",
	}
	for _, cmd := range safe {
		if got := Warning(cmd); got != "" {
			t.Errorf("Warning(%q) should be empty; got %q", cmd, got)
		}
	}
}

// Pattern ordering matters: `rm -rf` must hit the "force-remove
// recursively" branch even though it also matches the bare `-r` and
// `-f` patterns. The most-specific warning wins by virtue of
// appearing first in the table.
func TestWarningOrderingPicksMostSpecific(t *testing.T) {
	got := Warning("rm -rf foo")
	if !strings.Contains(got, "force-remove") {
		t.Errorf("rm -rf must surface the combined warning; got %q", got)
	}
	if strings.Contains(got, "may recursively remove files") {
		t.Errorf("rm -rf should NOT fall through to the broader -r warning; got %q", got)
	}
}
