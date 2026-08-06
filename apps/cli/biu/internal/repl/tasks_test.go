// Tests for the /tasks slash handler. The runtime store + the four
// subcommand branches are exercised; rendering is asserted on
// substring-match against the system note string the handler returns.

package repl

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/bgtask"
)

func TestTasksWithoutStoreSoftWarns(t *testing.T) {
	m := model{}
	got := m.handleTasks([]string{"/tasks"})
	if !strings.Contains(got, "background tasks aren't enabled") {
		t.Errorf("expected disabled-feature warning; got %q", got)
	}
}

func TestTasksListEmpty(t *testing.T) {
	m := model{bgTasks: bgtask.NewStore()}
	got := m.handleTasks([]string{"/tasks"})
	if !strings.Contains(got, "no background tasks") {
		t.Errorf("empty-state line missing; got %q", got)
	}
}

func TestTasksListShowsRows(t *testing.T) {
	store := bgtask.NewStore()
	defer store.StopAll()
	_, _ = store.Spawn(context.Background(), bgtask.SpawnRequest{Command: "echo hi"})
	_, _ = store.Spawn(context.Background(), bgtask.SpawnRequest{Command: "echo there"})
	// Wait so the tasks finish — keeps assertions deterministic.
	time.Sleep(150 * time.Millisecond)

	m := model{bgTasks: store}
	got := m.handleTasks([]string{"/tasks"})
	for _, want := range []string{"bg-1", "bg-2", "echo hi", "echo there", "done"} {
		if !strings.Contains(got, want) {
			t.Errorf("list missing %q; got %q", want, got)
		}
	}
}

func TestTasksOutputUnknownID(t *testing.T) {
	m := model{bgTasks: bgtask.NewStore()}
	got := m.handleTasks([]string{"/tasks", "output", "bg-9999"})
	if !strings.Contains(got, "no such task") {
		t.Errorf("unknown id should soft-warn; got %q", got)
	}
}

func TestTasksOutputShowsLines(t *testing.T) {
	store := bgtask.NewStore()
	task, _ := store.Spawn(context.Background(), bgtask.SpawnRequest{
		Command: `printf "alpha\nbeta\n"`,
	})
	<-task.Done()

	m := model{bgTasks: store}
	got := m.handleTasks([]string{"/tasks", "output", task.ID})
	for _, want := range []string{"alpha", "beta", "next-line"} {
		if !strings.Contains(got, want) {
			t.Errorf("output line missing %q; got %q", want, got)
		}
	}
}

func TestTasksOutputDeltaCursor(t *testing.T) {
	store := bgtask.NewStore()
	task, _ := store.Spawn(context.Background(), bgtask.SpawnRequest{
		Command: `printf "1\n2\n3\n4\n"`,
	})
	<-task.Done()

	m := model{bgTasks: store}
	got := m.handleTasks([]string{"/tasks", "output", task.ID, "2"})
	if strings.Contains(got, "\n1\n") {
		t.Errorf("since=2 should hide line 1; got %q", got)
	}
	if !strings.Contains(got, "next-line: 4") {
		t.Errorf("expected next-line: 4 after consuming 2 of 4; got %q", got)
	}
}

func TestTasksOutputBadCursor(t *testing.T) {
	m := model{bgTasks: bgtask.NewStore()}
	got := m.handleTasks([]string{"/tasks", "output", "bg-1", "abc"})
	if !strings.Contains(got, "must be an integer") {
		t.Errorf("non-integer cursor should error: %q", got)
	}
}

func TestTasksKill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signals only")
	}
	store := bgtask.NewStore()
	defer store.StopAll()

	task, _ := store.Spawn(context.Background(), bgtask.SpawnRequest{Command: "sleep 30"})
	time.Sleep(50 * time.Millisecond)

	m := model{bgTasks: store}
	got := m.handleTasks([]string{"/tasks", "kill", task.ID})
	if !strings.Contains(got, "killed") {
		t.Errorf("kill payload should report status killed; got %q", got)
	}
}

func TestTasksKillAll(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signals only")
	}
	store := bgtask.NewStore()
	defer store.StopAll()
	for i := 0; i < 3; i++ {
		_, _ = store.Spawn(context.Background(), bgtask.SpawnRequest{Command: "sleep 30"})
	}
	time.Sleep(50 * time.Millisecond)

	m := model{bgTasks: store}
	got := m.handleTasks([]string{"/tasks", "killall"})
	if !strings.Contains(got, "killed 3") {
		t.Errorf("killall should report 3; got %q", got)
	}
}

func TestTasksUnknownSubcommand(t *testing.T) {
	m := model{bgTasks: bgtask.NewStore()}
	got := m.handleTasks([]string{"/tasks", "foo"})
	if !strings.HasPrefix(got, "/tasks: usage:") {
		t.Errorf("unknown subcommand should print usage; got %q", got)
	}
}
