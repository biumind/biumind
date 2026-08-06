package chat

import (
	"testing"
)

func TestParseThreadModelParamsEmpty(t *testing.T) {
	got := parseThreadModelParams(nil)
	if got.Temperature != nil || got.TopP != nil || got.MaxTokens != 0 {
		t.Errorf("nil metadata should yield zero, got %+v", got)
	}

	got = parseThreadModelParams([]byte(``))
	if got.Temperature != nil {
		t.Errorf("empty string should yield zero, got %+v", got)
	}

	got = parseThreadModelParams([]byte(`{}`))
	if got.Temperature != nil {
		t.Errorf("empty object should yield zero, got %+v", got)
	}
}

func TestParseThreadModelParamsHappy(t *testing.T) {
	in := []byte(`{
		"model_params": {
			"temperature": 0.3,
			"top_p": 0.9,
			"max_tokens": 2048,
			"stop_sequences": ["END", "STOP"]
		}
	}`)
	got := parseThreadModelParams(in)
	if got.Temperature == nil || *got.Temperature != 0.3 {
		t.Errorf("temperature: %+v", got.Temperature)
	}
	if got.TopP == nil || *got.TopP != 0.9 {
		t.Errorf("top_p: %+v", got.TopP)
	}
	if got.MaxTokens != 2048 {
		t.Errorf("max_tokens: %d", got.MaxTokens)
	}
	if len(got.StopSequences) != 2 || got.StopSequences[0] != "END" {
		t.Errorf("stop_sequences: %+v", got.StopSequences)
	}
}

func TestParseThreadModelParamsMalformed(t *testing.T) {
	// Bad JSON should not panic — returns zero.
	got := parseThreadModelParams([]byte(`{model_params: garbage`))
	if got.MaxTokens != 0 || got.Temperature != nil {
		t.Errorf("malformed should yield zero, got %+v", got)
	}
}

func TestParseThreadModelParamsIgnoresUnknownKeys(t *testing.T) {
	// Other metadata keys must coexist; we only care about model_params.
	in := []byte(`{"other_key":"foo","model_params":{"max_tokens":100},"x":1}`)
	got := parseThreadModelParams(in)
	if got.MaxTokens != 100 {
		t.Errorf("max_tokens: %d", got.MaxTokens)
	}
}

func TestMergedModelParamsRequestWins(t *testing.T) {
	half := 0.5
	thread := modelParams{
		MaxTokens:   2048,
		Temperature: &half,
	}
	one := 1.0
	req := sendReq{
		MaxTokens:   500,
		Temperature: &one,
	}
	out := mergedModelParams(req, thread)
	if out.MaxTokens != 500 {
		t.Errorf("max_tokens: got %d want 500", out.MaxTokens)
	}
	if out.Temperature == nil || *out.Temperature != 1.0 {
		t.Errorf("temperature: %+v", out.Temperature)
	}
}

func TestMergedModelParamsThreadFallback(t *testing.T) {
	// Request leaves fields zero/nil; thread defaults fill them.
	half := 0.5
	thread := modelParams{
		MaxTokens:     2048,
		Temperature:   &half,
		StopSequences: []string{"X"},
	}
	out := mergedModelParams(sendReq{}, thread)
	if out.MaxTokens != 2048 {
		t.Errorf("max_tokens: got %d want 2048", out.MaxTokens)
	}
	if out.Temperature == nil || *out.Temperature != 0.5 {
		t.Errorf("temperature should fall through, got %+v", out.Temperature)
	}
	if len(out.StopSequences) != 1 || out.StopSequences[0] != "X" {
		t.Errorf("stop_sequences should fall through, got %+v", out.StopSequences)
	}
}

func TestMergedModelParamsZeroMaxTokensDoesNotOverride(t *testing.T) {
	// Subtle: req.MaxTokens=0 means "no override". Without this check
	// users pasting an empty number into the UI would clobber the
	// thread default with zero.
	thread := modelParams{MaxTokens: 4096}
	out := mergedModelParams(sendReq{MaxTokens: 0}, thread)
	if out.MaxTokens != 4096 {
		t.Errorf("zero should not override; got %d", out.MaxTokens)
	}
}
