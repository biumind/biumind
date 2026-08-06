package hooks

import "testing"

// TestAllEvents_IncludesNewExpansion locks the P20.55 event names so
// a future "rationalize the list" PR has to update this test
// explicitly. The set is the contract — settings.json validators and
// the hook runner both consult AllEvents.
func TestAllEvents_IncludesNewExpansion(t *testing.T) {
	want := []Event{
		EventStopFailure, EventSubagentStart, EventTeammateIdle,
		EventPermissionRequest, EventPermissionDenied,
		EventTaskCreated, EventTaskCompleted,
		EventFileChanged, EventCwdChanged,
	}
	have := map[Event]bool{}
	for _, e := range AllEvents {
		have[e] = true
	}
	for _, e := range want {
		if !have[e] {
			t.Errorf("AllEvents missing %q (P20.55 event)", e)
		}
		if !IsValid(string(e)) {
			t.Errorf("IsValid should accept %q", e)
		}
	}
}
