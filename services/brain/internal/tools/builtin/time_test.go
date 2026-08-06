package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/biumind/biumind/services/brain/internal/tools"
)

func TestTimeNowDescriptorMetadata(t *testing.T) {
	tool := TimeNow()
	if tool.Name != "time_now" {
		t.Errorf("name: got %q want time.now", tool.Name)
	}
	if !tool.Runtime.AvailableIn(tools.ExecutionCloud) ||
		!tool.Runtime.AvailableIn(tools.ExecutionClient) {
		t.Errorf("expected RuntimeBoth, got %s", tool.Runtime)
	}
	if tool.Invoke == nil {
		t.Fatal("expected Invoke func, got nil")
	}
	// Schema must be valid JSON or AgentLoop's model-relay forwarder will
	// quietly drop the parameter object.
	var schema map[string]any
	if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
		t.Fatalf("schema invalid json: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("schema type: got %v want object", schema["type"])
	}
}

func TestTimeNowDefaultsUTC(t *testing.T) {
	tool := TimeNow()
	got, err := tool.Invoke(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	m := got.(map[string]any)
	if m["timezone"] != "UTC" {
		t.Errorf("timezone: got %v want UTC", m["timezone"])
	}
	iso, _ := m["iso"].(string)
	if !strings.Contains(iso, "T") {
		t.Errorf("iso shape: got %q", iso)
	}
}

func TestTimeNowResolvesTimezone(t *testing.T) {
	tool := TimeNow()
	got, err := tool.Invoke(context.Background(),
		json.RawMessage(`{"timezone":"Asia/Shanghai"}`))
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	m := got.(map[string]any)
	if m["timezone"] != "Asia/Shanghai" {
		t.Errorf("timezone: got %v want Asia/Shanghai", m["timezone"])
	}
}

func TestTimeNowFallsBackOnBadTimezone(t *testing.T) {
	tool := TimeNow()
	got, err := tool.Invoke(context.Background(),
		json.RawMessage(`{"timezone":"NotARealZone"}`))
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	m := got.(map[string]any)
	if m["timezone"] != "UTC" {
		t.Errorf("timezone: got %v want UTC", m["timezone"])
	}
}

func TestTimeNowNilInputDoesntPanic(t *testing.T) {
	tool := TimeNow()
	_, err := tool.Invoke(context.Background(), nil)
	if err != nil {
		t.Errorf("invoke nil: %v", err)
	}
}

func TestTimeNowUnixIsRecent(t *testing.T) {
	tool := TimeNow()
	got, _ := tool.Invoke(context.Background(), json.RawMessage(`{}`))
	m := got.(map[string]any)
	unix, ok := m["unix"].(int64)
	if !ok {
		t.Fatalf("unix not int64: %T", m["unix"])
	}
	now := time.Now().Unix()
	if delta := now - unix; delta < -2 || delta > 2 {
		t.Errorf("unix drift too large: now=%d tool=%d", now, unix)
	}
}
