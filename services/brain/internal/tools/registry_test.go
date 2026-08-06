package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestRuntimeAvailability(t *testing.T) {
	cases := []struct {
		rt    Runtime
		mode  ExecutionMode
		ok    bool
	}{
		{RuntimeCloud, ExecutionCloud, true},
		{RuntimeCloud, ExecutionClient, false},
		{RuntimeClient, ExecutionCloud, false},
		{RuntimeClient, ExecutionClient, true},
		{RuntimeBoth, ExecutionCloud, true},
		{RuntimeBoth, ExecutionClient, true},
	}
	for _, c := range cases {
		if got := c.rt.AvailableIn(c.mode); got != c.ok {
			t.Errorf("rt=%s mode=%s: got %v want %v",
				c.rt, c.mode, got, c.ok)
		}
	}
}

func TestRuntimeJSON(t *testing.T) {
	for _, rt := range []Runtime{RuntimeCloud, RuntimeClient, RuntimeBoth} {
		b, err := json.Marshal(rt)
		if err != nil {
			t.Fatalf("marshal %s: %v", rt, err)
		}
		var back Runtime
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatalf("unmarshal %s: %v", b, err)
		}
		if back != rt {
			t.Errorf("roundtrip: got %s want %s", back, rt)
		}
	}
	var bad Runtime
	if err := json.Unmarshal([]byte(`"nowhere"`), &bad); err == nil {
		t.Error("expected error on unknown runtime string")
	}
}

func TestRegistryRegisterAndGet(t *testing.T) {
	r := New()
	tool := Tool{
		Descriptor: Descriptor{
			Name:        "websearch",
			Description: "Search the web",
			Source:      "builtin",
			Runtime:     RuntimeBoth,
		},
	}
	if err := r.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}
	got, ok := r.Get("websearch")
	if !ok {
		t.Fatal("expected tool present")
	}
	if got.Name != tool.Name || got.Runtime != tool.Runtime {
		t.Errorf("descriptor mismatch: %+v", got)
	}
	if err := r.Register(tool); !errors.Is(err, ErrDuplicate) {
		t.Errorf("duplicate: got %v want ErrDuplicate", err)
	}
}

func TestRegistryRegisterValidation(t *testing.T) {
	r := New()
	if err := r.Register(Tool{Descriptor: Descriptor{Runtime: RuntimeCloud}}); !errors.Is(err, ErrEmptyName) {
		t.Errorf("empty name: got %v", err)
	}
	if err := r.Register(Tool{Descriptor: Descriptor{Name: "x"}}); !errors.Is(err, ErrInvalidRT) {
		t.Errorf("zero runtime: got %v", err)
	}
}

func TestRegistryAvailableFiltersByMode(t *testing.T) {
	r := New()
	r.MustRegister(Tool{Descriptor: Descriptor{Name: "wiki.search", Runtime: RuntimeCloud}})
	r.MustRegister(Tool{Descriptor: Descriptor{Name: "fs.read", Runtime: RuntimeClient}})
	r.MustRegister(Tool{Descriptor: Descriptor{Name: "websearch", Runtime: RuntimeBoth}})

	cloud := r.AvailableNames(ExecutionCloud)
	want := []string{"websearch", "wiki.search"}
	if !equalStrings(cloud, want) {
		t.Errorf("cloud: got %v want %v", cloud, want)
	}

	client := r.AvailableNames(ExecutionClient)
	want = []string{"fs.read", "websearch"}
	if !equalStrings(client, want) {
		t.Errorf("client: got %v want %v", client, want)
	}
}

func TestRegistryAllSorted(t *testing.T) {
	r := New()
	r.MustRegister(Tool{Descriptor: Descriptor{Name: "z", Runtime: RuntimeBoth}})
	r.MustRegister(Tool{Descriptor: Descriptor{Name: "a", Runtime: RuntimeBoth}})
	r.MustRegister(Tool{Descriptor: Descriptor{Name: "m", Runtime: RuntimeBoth}})
	got := r.All()
	if len(got) != 3 || got[0].Name != "a" || got[1].Name != "m" || got[2].Name != "z" {
		t.Errorf("not sorted: %+v", got)
	}
}

func TestRegistryInvokeRoundTrip(t *testing.T) {
	r := New()
	r.MustRegister(Tool{
		Descriptor: Descriptor{Name: "echo", Runtime: RuntimeCloud},
		Invoke: func(_ context.Context, in json.RawMessage) (any, error) {
			return map[string]any{"got": string(in)}, nil
		},
	})
	got, err := r.Invoke(context.Background(), ExecutionCloud, "echo",
		json.RawMessage(`{"x":1}`))
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	m := got.(map[string]any)
	if m["got"] != `{"x":1}` {
		t.Errorf("unexpected payload: %v", m)
	}
}

func TestRegistryInvokeUnknown(t *testing.T) {
	r := New()
	_, err := r.Invoke(context.Background(), ExecutionCloud, "nope", nil)
	if !errors.Is(err, ErrUnknownTool) {
		t.Errorf("got %v want ErrUnknownTool", err)
	}
}

func TestRegistryInvokeWrongMode(t *testing.T) {
	r := New()
	r.MustRegister(Tool{
		Descriptor: Descriptor{Name: "fs.read", Runtime: RuntimeClient},
		Invoke: func(_ context.Context, _ json.RawMessage) (any, error) {
			return "should not run", nil
		},
	})
	_, err := r.Invoke(context.Background(), ExecutionCloud, "fs.read", nil)
	if !errors.Is(err, ErrInvalidRT) {
		t.Errorf("got %v want ErrInvalidRT", err)
	}
}

func TestRegistryInvokeDescriptorOnly(t *testing.T) {
	r := New()
	if err := r.RegisterDescriptor(Descriptor{
		Name: "client.fs", Runtime: RuntimeClient,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	_, err := r.Invoke(context.Background(), ExecutionClient, "client.fs", nil)
	if !errors.Is(err, ErrNotInvocable) {
		t.Errorf("got %v want ErrNotInvocable", err)
	}
}

func TestValidExecutionMode(t *testing.T) {
	for _, m := range []string{"cloud", "client"} {
		if !ValidExecutionMode(m) {
			t.Errorf("expected %q valid", m)
		}
	}
	for _, m := range []string{"", "hybrid", "CLOUD"} {
		if ValidExecutionMode(m) {
			t.Errorf("expected %q invalid", m)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
