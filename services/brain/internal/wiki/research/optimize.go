// Research topic optimizer (POST .../research/optimize).
//
// 用户给一句话话题，LLM 展开成规范 topic + 3-5 条搜索查询，客户端据此
// 预填 research 创建表单（用户可改后再 POST /research）。对照
// reference llm_wiki/src/lib/optimize-research-topic.ts —— 那边是
// TOPIC:/QUERY: 行格式，这里要求严格 JSON 输出，解析更稳。
//
// LLM 调用走 Orchestrator 的 LLMCaller（model-relay 内部车道，计费归
// owner —— 与 synthesize 同一条路）。
package research

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// OptimizeResult is the wire shape returned by the optimize endpoint.
type OptimizeResult struct {
	Topic   string   `json:"topic"`
	Queries []string `json:"queries"`
}

const optimizeSystemPrompt = `You are a research assistant. The user gives a rough one-line research topic; expand it into a precise research topic and a set of web search queries.

## MANDATORY OUTPUT LANGUAGE
- The "topic" field MUST be in the same language as the user's input.
- Search queries may use English keywords when they better match search engines.

## Query Rules
- 3 to 5 queries, each keyword-rich and specific — not generic.
- Cover different angles of the topic (definition, state of the art, comparison, practice).

## Output Format (STRICT)
Respond with ONLY a JSON object, no markdown fences, no explanation:
{"topic": "<one precise sentence>", "queries": ["<query 1>", "<query 2>", "<query 3>"]}
`

// maxOptimizeQueries caps the query list the endpoint returns. The LLM
// is asked for 3-5; anything beyond is dropped, anything empty filtered.
const maxOptimizeQueries = 5

// OptimizeTopic expands a raw one-line topic into {topic, queries} via
// the orchestrator's LLM caller. Returns an error when the LLM output
// can't be parsed into a usable result — the caller maps that to 502
// (the LLM call itself failing is also an error → 502/503 upstream).
func (o *Orchestrator) OptimizeTopic(ctx context.Context, ownerID uuid.UUID, rawTopic string) (*OptimizeResult, error) {
	if o.llm == nil {
		return nil, errors.New("research optimize: LLM not configured")
	}
	raw, err := o.llm.Chat(ctx, ownerID, optimizeSystemPrompt,
		"Research topic: "+rawTopic)
	if err != nil {
		return nil, fmt.Errorf("llm: %w", err)
	}
	return parseOptimizeOutput(raw, rawTopic)
}

// parseOptimizeOutput parses the model's JSON answer into an
// OptimizeResult. Tolerates <think> leakage (same cleanThinking as
// synthesize) and ```json fences; falls back to the user's raw topic
// when the model's topic is empty, and to [topic] when no usable query
// survived (mirrors the reference's fallback chain).
func parseOptimizeOutput(raw, fallbackTopic string) (*OptimizeResult, error) {
	cleaned := cleanThinking(raw)
	// Strip a single wrapping code fence if the model added one anyway.
	if i := strings.Index(cleaned, "{"); i >= 0 {
		if j := strings.LastIndex(cleaned, "}"); j > i {
			cleaned = cleaned[i : j+1]
		}
	}
	var out struct {
		Topic   string   `json:"topic"`
		Queries []string `json:"queries"`
	}
	if err := json.Unmarshal([]byte(cleaned), &out); err != nil {
		return nil, fmt.Errorf("parse optimize output: %w", err)
	}
	topic := strings.TrimSpace(out.Topic)
	if topic == "" {
		topic = fallbackTopic
	}
	queries := make([]string, 0, len(out.Queries))
	for _, q := range out.Queries {
		q = strings.TrimSpace(q)
		if q == "" {
			continue
		}
		queries = append(queries, q)
		if len(queries) >= maxOptimizeQueries {
			break
		}
	}
	if len(queries) == 0 {
		queries = []string{topic}
	}
	return &OptimizeResult{Topic: topic, Queries: queries}, nil
}
