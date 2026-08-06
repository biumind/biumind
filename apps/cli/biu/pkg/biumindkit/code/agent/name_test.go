package agent

import (
	"context"
	"errors"
	"testing"
)

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"  修复登录崩溃  ":      "修复登录崩溃",
		"\"重构支付模块\"":      "重构支付模块",
		"`add dark mode`": "add dark mode",
		"实现搜索功能。":         "实现搜索功能",
		"标题\n第二行解释":       "标题",
		"“给按钮加圆角”":        "给按钮加圆角",
	}
	for in, want := range cases {
		if got := sanitizeName(in); got != want {
			t.Errorf("sanitizeName(%q)=%q want %q", in, got, want)
		}
	}
}

func TestGenerateName_nilGen(t *testing.T) {
	if _, err := GenerateName(context.Background(), nil, "x"); err == nil {
		t.Error("expected error for nil generator")
	}
}

func TestGenerateName_emptyPrompt(t *testing.T) {
	gen := func(context.Context, string) (string, error) { return "x", nil }
	if _, err := GenerateName(context.Background(), gen, "   "); err == nil {
		t.Error("expected error for empty prompt")
	}
}

func TestGenerateName_callsGenAndSanitizes(t *testing.T) {
	gen := func(_ context.Context, prompt string) (string, error) {
		if prompt == "" {
			return "", errors.New("empty")
		}
		return "  \"清理死代码\"。  ", nil
	}
	got, err := GenerateName(context.Background(), gen, "删掉所有 codeSync 残留")
	if err != nil {
		t.Fatal(err)
	}
	if got != "清理死代码" {
		t.Errorf("got %q want 清理死代码", got)
	}
}
