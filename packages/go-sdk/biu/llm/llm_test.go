package llm

import (
	"errors"
	"testing"
)

func TestCollectText(t *testing.T) {
	ch := make(chan Frame, 5)
	ch <- Frame{Kind: KindDelta, Text: "Hello"}
	ch <- Frame{Kind: KindDelta, Text: ", world"}
	ch <- Frame{Kind: KindStop, Stop: "end_turn"}
	ch <- Frame{Kind: KindEnd}
	close(ch)
	out, err := CollectText(ch)
	if err != nil {
		t.Fatal(err)
	}
	if out != "Hello, world" {
		t.Errorf("got %q", out)
	}
}

func TestCollectTextSurfacesError(t *testing.T) {
	ch := make(chan Frame, 3)
	ch <- Frame{Kind: KindDelta, Text: "partial"}
	ch <- Frame{Kind: KindError, Err: errors.New("boom")}
	close(ch)
	out, err := CollectText(ch)
	if err == nil || err.Error() != "boom" {
		t.Errorf("err = %v", err)
	}
	if out != "partial" {
		t.Errorf("partial text lost: %q", out)
	}
}
