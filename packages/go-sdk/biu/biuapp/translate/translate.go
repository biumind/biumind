// Package translate — language translation via the platform's
// llm.Provider, exposed as a BiuApp.
//
// Action: `translate`
//
//	in:  {"text": "...", "target": "en", "source": "auto"}
//	out: {"text": "...", "source": "zh", "target": "en", "model": "..."}
//
// The app stays decoupled from any specific LLM; whoever constructs it
// passes the Provider in. Tests use a fake Provider for deterministic
// assertions.
package translate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
	"github.com/biumind/biumind/packages/go-sdk/biu/llm"
)

const Name = "translate"

type App struct {
	provider llm.Provider
	model    string
}

// New builds the app. `model` is forwarded to the provider; pass an
// empty string to let the provider's default win.
func New(provider llm.Provider, model string) *App {
	return &App{provider: provider, model: model}
}

func (a *App) Manifest() biuapp.Manifest {
	return biuapp.Manifest{
		Name:        Name,
		Version:     "0.2.0",
		Description: "Translate text using the configured LLM provider",
		Author:      "BiuMind",
		Permissions: []string{"hub.invoke"},
		Actions: []biuapp.ActionSpec{
			{
				Name:        "translate",
				Description: "Translate text into the target language. source='auto' lets the model detect.",
				Risk:        biuapp.RiskMedium, // hits LLM, costs tokens
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"text"},
					"properties": map[string]any{
						"text": map[string]any{"type": "string", "title": "文本"},
						"target": map[string]any{
							"type": "string", "default": "en", "title": "目标语言",
							"enum": []string{"en", "zh", "ja", "ko", "fr", "de", "es", "it", "ru"},
						},
						"source": map[string]any{
							"type": "string", "default": "auto", "title": "源语言",
							"enum": []string{"auto", "en", "zh", "ja", "ko", "fr", "de", "es", "it", "ru"},
						},
					},
				},
			},
		},
		ManifestExt: biuapp.ManifestExt{
			Identifier: Name,
			Title:      "翻译",
			Category:   "utility",
			Kind:       "hybrid",
			Views: []biuapp.ViewSpec{
				{
					ID:        "home",
					Route:     "/apps/translate",
					Title:     "翻译",
					Layout:    biuapp.LayoutForm,
					SchemaRef: "actions.translate.input_schema",
					Submit: &biuapp.FormSubmit{
						Action:    "translate",
						OnSuccess: &biuapp.ViewActionEffect{Toast: "翻译完成"},
					},
				},
			},
			Sidebar: &biuapp.SidebarHints{
				PreferredPosition: "bottom",
			},
		},
	}
}

func (a *App) Init(ctx context.Context, deps biuapp.Deps) error {
	if a.provider == nil {
		return errors.New("translate: provider is nil")
	}
	return nil
}

type input struct {
	Text   string `json:"text"`
	Target string `json:"target"`
	Source string `json:"source"`
}

type Output struct {
	Text   string `json:"text"`
	Source string `json:"source"`
	Target string `json:"target"`
	Model  string `json:"model,omitempty"`
}

func (a *App) Invoke(ctx context.Context, action string, raw json.RawMessage) (any, error) {
	if action != "translate" {
		return nil, fmt.Errorf("translate: unknown action %q", action)
	}
	var in input
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("translate: bad input: %w", err)
	}
	if strings.TrimSpace(in.Text) == "" {
		return nil, errors.New("translate: empty text")
	}
	if in.Target == "" {
		in.Target = "en"
	}
	if in.Source == "" {
		in.Source = "auto"
	}

	system := fmt.Sprintf(
		"You are a precise translator. Translate the user's text into %s. "+
			"Source language is %s — detect it if 'auto'. Output ONLY the translation, "+
			"no commentary, no quotes, no language hints.",
		in.Target, in.Source,
	)

	frames, err := a.provider.ChatStream(ctx, llm.ChatRequest{
		Model:  a.model,
		System: system,
		Messages: []llm.Message{
			{Role: "user", Content: in.Text},
		},
		MaxTokens: 4096,
	})
	if err != nil {
		return nil, fmt.Errorf("translate: provider: %w", err)
	}
	text, err := llm.CollectText(frames)
	if err != nil {
		return nil, fmt.Errorf("translate: stream: %w", err)
	}
	return Output{
		Text:   strings.TrimSpace(text),
		Source: in.Source,
		Target: in.Target,
		Model:  a.model,
	}, nil
}
