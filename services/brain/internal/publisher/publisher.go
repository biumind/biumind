// Package publisher emits events to the Realtime service via internal RPC.
package publisher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

type Publisher interface {
	Publish(ctx context.Context, topic, kind string, payload map[string]any) error
}

type Realtime struct {
	URL    string
	HC     *http.Client
	Logger *slog.Logger
}

func NewRealtime(url string, logger *slog.Logger) *Realtime {
	return &Realtime{URL: url, HC: &http.Client{Timeout: 3 * time.Second}, Logger: logger}
}

func (r *Realtime) Publish(ctx context.Context, topic, kind string, payload map[string]any) error {
	if r.URL == "" {
		return nil
	}
	body, _ := json.Marshal(map[string]any{
		"topic": topic, "kind": kind, "payload": payload,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.URL+"/v1/internal/publish", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.HC.Do(req)
	if err != nil {
		return fmt.Errorf("publisher: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("publisher: status %d", resp.StatusCode)
	}
	return nil
}

type Noop struct{}

func (Noop) Publish(ctx context.Context, topic, kind string, payload map[string]any) error {
	return nil
}

type Memory struct {
	Events []Captured
}

type Captured struct {
	Topic   string
	Kind    string
	Payload map[string]any
}

func (m *Memory) Publish(ctx context.Context, topic, kind string, payload map[string]any) error {
	m.Events = append(m.Events, Captured{Topic: topic, Kind: kind, Payload: payload})
	return nil
}
