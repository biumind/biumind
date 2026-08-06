package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/gitassist"
)

// GenerateName 用注入的 gen(走 model-relay,满足 I6)把任务 prompt 概括成一个短
// 标题。gen 为 nil → 返回错误,
// 由调用方回退到 prompt 截断。
//
// 不直连任何 LLM SDK:gen 是 bridge 经 SetCommitGenerator 注入的同一条 model-relay
// 缝,这里复用它做"概括"用途。
func GenerateName(ctx context.Context, gen gitassist.Generator, taskPrompt string) (string, error) {
	if gen == nil {
		return "", fmt.Errorf("agent: name generator unavailable")
	}
	p := strings.TrimSpace(taskPrompt)
	if p == "" {
		return "", fmt.Errorf("agent: empty prompt")
	}
	ask := "用一句不超过 20 个字的短语概括下面的编码任务,作为任务标题。" +
		"只输出标题本身,不要引号、标点、emoji 或任何前后缀。用与任务相同的语言。\n\n任务:\n" + p
	out, err := gen(ctx, ask)
	if err != nil {
		return "", err
	}
	return sanitizeName(out), nil
}

// sanitizeName 清洗模型输出:取首行、去引号/反引号、去首尾标点与空白,限长 ~60 字符。
func sanitizeName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// 取首个非空行(模型可能多行解释)。
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			s = line
			break
		}
	}
	// 反复从两端去引号 / 反引号 / 标点 / 空白,直到稳定(处理 "标题"。 这类引号夹标点)。
	const cutset = "\"'`“”‘’。.!！?？:：;；,，、 \t"
	for {
		t := strings.Trim(s, cutset)
		if t == s {
			break
		}
		s = t
	}
	// 限长(按 rune,避免截断多字节)。
	const maxRunes = 60
	r := []rune(s)
	if len(r) > maxRunes {
		s = string(r[:maxRunes]) + "…"
	}
	return s
}
