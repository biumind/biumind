// Stub driver — for unit tests. Records every Send and lets the test
// inject pre-built inbound envelopes.
package driver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/biumind/biumind/services/channels/internal/envelope"
	"github.com/google/uuid"
)

type Stub struct {
	mu   sync.Mutex
	Sent []envelope.Envelope
	// FailNext makes the next Send() return an error.
	FailNext bool
}

func NewStub() *Stub { return &Stub{} }

func (s *Stub) Name() string { return "stub" }

// VerifyAndParse for stub treats the request body as a JSON Envelope
// (single or array). No signature check. Tests use it to push canned
// inbound envelopes through the API/router stack.
func (s *Stub) VerifyAndParse(r *http.Request) ([]envelope.Envelope, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, errors.New("empty body")
	}
	if body[0] == '[' {
		var arr []envelope.Envelope
		if err := json.Unmarshal(body, &arr); err != nil {
			return nil, err
		}
		for i := range arr {
			arr[i].Channel = s.Name()
			arr[i].Direction = envelope.DirectionInbound
			if arr[i].MessageID == "" {
				arr[i].MessageID = "stub-" + uuid.NewString()
			}
		}
		return arr, nil
	}
	var single envelope.Envelope
	if err := json.Unmarshal(body, &single); err != nil {
		return nil, err
	}
	single.Channel = s.Name()
	single.Direction = envelope.DirectionInbound
	if single.MessageID == "" {
		single.MessageID = "stub-" + uuid.NewString()
	}
	return []envelope.Envelope{single}, nil
}

func (s *Stub) Send(ctx context.Context, e envelope.Envelope) (envelope.Envelope, error) {
	if s.FailNext {
		s.FailNext = false
		return e, errors.New("stub: forced failure")
	}
	e.Channel = s.Name()
	e.Direction = envelope.DirectionOutbound
	e.MessageID = "stub-out-" + uuid.NewString()
	if e.SentAt.IsZero() {
		e.SentAt = time.Now().UTC()
	}
	s.mu.Lock()
	s.Sent = append(s.Sent, e)
	s.mu.Unlock()
	return e, nil
}
