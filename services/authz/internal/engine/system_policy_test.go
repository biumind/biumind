package engine

import (
	"os"
	"path/filepath"
	"testing"
)

// systemEngine loads deploy/.../policies/policies.cedar so the test
// always runs against the shipped policy, not a string copy.
func systemEngine(t *testing.T) *Engine {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(wd, "..", "..", "..", "..")
	p := filepath.Join(root, "deploy", "docker-compose", "authz", "policies", "policies.cedar")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read policies.cedar: %v", err)
	}
	e := New()
	if err := e.LoadPolicies(raw); err != nil {
		t.Fatalf("load policies.cedar: %v", err)
	}
	return e
}

// topicResource mirrors the realtime service's authz request shape
// (services/realtime/internal/authz: Topic entity with kind/id/owner
// attributes; owner defaults to the subscribing principal).
func topicResource(kind, id, owner string) Entity {
	return Entity{
		Type: "Topic", ID: kind + ":" + id,
		Attributes: map[string]any{"kind": kind, "id": id, "owner": owner},
	}
}

func topicPrincipal(uid string) Entity {
	return Entity{Type: "User", ID: uid, Attributes: map[string]any{"id": uid}}
}

// chat:user:<uid> is self-only — cross-device chat sync events.
func TestRealtimeTopic_ChatUserSelfAllowed(t *testing.T) {
	e := systemEngine(t)
	res, err := e.Check(Input{
		Principal: topicPrincipal("u-1"),
		Action:    "realtime:Topic::subscribe",
		Resource:  topicResource("chat:user", "u-1", "u-1"),
	})
	assertDecision(t, res, err, DecisionAllow, "chat:user self subscribe")
}

func TestRealtimeTopic_ChatUserOtherDenied(t *testing.T) {
	e := systemEngine(t)
	res, err := e.Check(Input{
		Principal: topicPrincipal("u-2"),
		Action:    "realtime:Topic::subscribe",
		Resource:  topicResource("chat:user", "u-1", "u-1"),
	})
	assertDecision(t, res, err, DecisionDeny, "chat:user other subscribe")
}

// Existing self-only topics must keep working alongside the new clause.
func TestRealtimeTopic_NotifyUserSelfAllowed(t *testing.T) {
	e := systemEngine(t)
	res, err := e.Check(Input{
		Principal: topicPrincipal("u-1"),
		Action:    "realtime:Topic::subscribe",
		Resource:  topicResource("notify:user", "u-1", "u-1"),
	})
	assertDecision(t, res, err, DecisionAllow, "notify:user self subscribe")
}
