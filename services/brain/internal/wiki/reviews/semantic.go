// semantic.go — LLM 驱动的语义 lint（on-demand）。
//
// 与结构规则（runStructuralRules，纯 SQL）互补：语义规则需跨页理解，
// 由 LLM 一次看全项目摘要判定 contradiction / stale / missing-page /
// suggestion。思路参考 reference/llm_wiki（全项目页摘要拼一个 prompt
// 一次调用 + ---LINT--- 块协议），仅参考思路不 fork 代码。
//
// 触发：POST /v1/wiki/projects/{pid}/lint/semantic（handleSemantic）
//
//	→ go SemanticRunner.Run()，非阻塞，立即 202。
//	结果写 brain.review_items（contradiction → kind=contradiction，其余三类
//	stale/missing-page/suggestion → kind=lint；payload.rule_family=semantic），
//	与结构 lint / dedup / sweep 共队，前端 reviews_page「质量」tab 零改动展示。
//
// 落地形态 S+B1：进程内 goroutine 直接跑（仿 deep research 模式），
// 删 lint_run.semantic_requested 事件（原 emit 是发到真空的死代码 ——
// brain.events outbox 纯 publisher 无消费者，详 gap-analysis B-11）。
//
// 未配 MODEL_RELAY_URL / JWT_SECRET → NoopSemanticCaller，handleSemantic
// 返回 503 提示未启用。
package reviews

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 语义 finding 4 类（对齐 reference/llm_wiki lint.ts 的 prompt 分类）。
const (
	semanticCategoryContradiction = "contradiction"
	semanticCategoryStale         = "stale"
	semanticCategoryMissingPage   = "missing-page"
	semanticCategorySuggestion    = "suggestion"
)

// reviewTypeByCategory 把语义分类映射到前端 review_type_config 已支持
// 的徽章（contradiction / suggestion / missing-page）。stale 无对应徽章，
// 留空 —— reviews_page 会退化为默认渲染（不报错）。
var reviewTypeByCategory = map[string]string{
	semanticCategoryContradiction: "contradiction",
	semanticCategoryMissingPage:   "missing-page",
	semanticCategorySuggestion:    "suggestion",
	// stale: 无对应 → 空
}

// SemanticFinding 是一次语义分析产出的单条问题。
type SemanticFinding struct {
	Category string   // contradiction|stale|missing-page|suggestion（LLM 原样，后续 norm）
	Severity string   // warning|info
	Title    string   // 短标题（missing-page 应为概念名）
	Detail   string   // 描述
	Pages    []string // 引用到的页标题（PAGES: 行）
}

// pageSummary 是喂给 LLM 的单页摘要。
type pageSummary struct {
	ID    uuid.UUID
	Title string
	Body  string // content::text 前 N 字
}

// SemanticCaller 抽象 LLM 调用，便于 Noop / 测试替身。
type SemanticCaller interface {
	// Analyze 接收全项目页摘要，返回语义问题列表。
	// 未配置 relay 时应返回 (nil, nil)（降级，非错误）。
	Analyze(ctx context.Context, ownerID uuid.UUID, pages []pageSummary) ([]SemanticFinding, error)
}

// NoopSemanticCaller 永远返回空 —— 未配 model-relay 时用，语义 lint 静默禁用。
type NoopSemanticCaller struct{}

func (NoopSemanticCaller) Analyze(_ context.Context, _ uuid.UUID, _ []pageSummary) ([]SemanticFinding, error) {
	return nil, nil
}

// RelaySemanticCaller 走 model-relay /v1/messages（I6 不变量：业务服务
// 不直连 LLM SDK）。ownerID 注入 JWT sub，relay 侧解析该用户 BYOK /
// platform-pool 凭据 —— 与 reviews.HubLLMFilter / enrich.RelayLLMCaller 同模式。
type RelaySemanticCaller struct {
	RelayURL        string
	Model           string
	Signer          *bauth.Signer
	HTTP            *http.Client
	Timeout         time.Duration
	Logger          *slog.Logger
	SnippetMaxChars int // 单页摘要 body 截断（默认 500，仿 llm_wiki）
	MaxPages        int // 单次喂给 LLM 的最大页数（防爆 prompt，默认 60）
}

// NewRelaySemanticCaller 构造一个走 model-relay 的语义分析 caller。
func NewRelaySemanticCaller(relayURL, model string, signer *bauth.Signer, logger *slog.Logger) *RelaySemanticCaller {
	return &RelaySemanticCaller{
		RelayURL:        strings.TrimRight(relayURL, "/"),
		Model:           model,
		Signer:          signer,
		HTTP:            &http.Client{Timeout: 90 * time.Second},
		Timeout:         75 * time.Second,
		Logger:          logger,
		SnippetMaxChars: 500,
		MaxPages:        60,
	}
}

// SemanticRunner 编排一次语义 lint：fetch 摘要 → LLM → 后过滤 → 写 review_items。
type SemanticRunner struct {
	Pool              *pgxpool.Pool
	Reviews           *Store
	Caller            SemanticCaller
	Logger            *slog.Logger
	Timeout           time.Duration // 整次 Run 的预算（默认 5min）
	MaxOpenPerProject int           // open review cap（仿 lint_worker，默认 200）

	// inflight 防同项目重入：用户连点按钮只跑一个，避免并发 Upsert 撞
	// review_items.dedupe_key UNIQUE。
	inflight sync.Map
}

// NewSemanticRunner 构造 runner。nil Caller → Noop（语义 lint 禁用）。
func NewSemanticRunner(pool *pgxpool.Pool, store *Store, caller SemanticCaller, logger *slog.Logger) *SemanticRunner {
	if caller == nil {
		caller = NoopSemanticCaller{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &SemanticRunner{
		Pool:              pool,
		Reviews:           store,
		Caller:            caller,
		Logger:            logger,
		Timeout:           5 * time.Minute,
		MaxOpenPerProject: 200,
	}
}

// ErrSemanticAlreadyRunning 同项目已有一次语义 lint 在跑。
var ErrSemanticAlreadyRunning = fmt.Errorf("semantic lint already running for project")

// Run 执行一次语义 lint。应为 goroutine 调用（context.Background 派生），
// 不阻塞 HTTP handler。per-project 重入直接返回 ErrSemanticAlreadyRunning。
func (r *SemanticRunner) Run(ctx context.Context, projectID, ownerID uuid.UUID) error {
	if _, loaded := r.inflight.LoadOrStore(projectID, struct{}{}); loaded {
		return ErrSemanticAlreadyRunning
	}
	defer r.inflight.Delete(projectID)

	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	rctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// 上层 ctx 取消（进程退出）也尽快中止 —— 选 rctx 与 ctx 先到者。
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-rctx.Done():
		}
	}()

	r.Logger.Info("semantic lint started",
		"project_id", projectID, "timeout", timeout)

	// open review cap：队列太满就不再灌（仿 lint_worker.go）。
	if r.MaxOpenPerProject > 0 && r.Reviews != nil {
		open, err := r.Reviews.CountOpen(rctx, projectID)
		if err != nil {
			r.Logger.Warn("semantic lint: count open failed", "err", err)
		} else if open >= r.MaxOpenPerProject {
			r.Logger.Info("semantic lint skipped: open review cap reached",
				"project_id", projectID, "open", open, "cap", r.MaxOpenPerProject)
			return nil
		}
	}

	pages, titleMap, err := r.fetchSummaries(rctx, projectID)
	if err != nil {
		r.Logger.Warn("semantic lint: fetch summaries failed",
			"project_id", projectID, "err", err)
		return err
	}
	if len(pages) == 0 {
		r.Logger.Info("semantic lint: no pages, nothing to analyze",
			"project_id", projectID)
		return nil
	}

	findings, err := r.Caller.Analyze(rctx, ownerID, pages)
	if err != nil {
		r.Logger.Warn("semantic lint: LLM analyze failed",
			"project_id", projectID, "err", err)
		return err
	}
	before := len(findings)
	findings = r.postFilter(findings, titleMap)
	r.Logger.Info("semantic lint: analyzed",
		"project_id", projectID, "pages", len(pages),
		"findings_raw", before, "findings_after_filter", len(findings))

	written := 0
	for _, f := range findings {
		if r.upsertFinding(rctx, projectID, ownerID, f, titleMap) {
			written++
		}
	}
	r.Logger.Info("semantic lint done",
		"project_id", projectID, "written", written)
	return nil
}

// fetchSummaries 取全项目页摘要（title + blocks.content::text 前 N 字）
// + 建 normTitle→pageID 映射供 finding PAGES 解析与 missing-page 后过滤。
//
// body 用 content::text —— blocks.content 是 jsonb（含块结构噪音），但对
// 语义判定（主题 / 矛盾 / 缺页）足够；LATERAL 顺序按 block.position。
func (r *SemanticRunner) fetchSummaries(ctx context.Context, projectID uuid.UUID) ([]pageSummary, map[string]uuid.UUID, error) {
	cap := 500
	if rc, ok := r.Caller.(*RelaySemanticCaller); ok && rc.SnippetMaxChars > 0 {
		cap = rc.SnippetMaxChars
	}
	maxPages := 60
	if rc, ok := r.Caller.(*RelaySemanticCaller); ok && rc.MaxPages > 0 {
		maxPages = rc.MaxPages
	}

	rows, err := r.Pool.Query(ctx, `
		SELECT p.id, p.title,
		       COALESCE(string_agg(b.content::text, E'\n'
		                            ORDER BY b.position), '') AS body
		FROM brain.pages p
		LEFT JOIN brain.blocks b
		  ON b.page_id = p.id AND b.deleted_at IS NULL
		WHERE p.project_id = $1 AND p.deleted_at IS NULL
		GROUP BY p.id, p.title
		ORDER BY p.updated_at DESC
		LIMIT $2
	`, projectID, maxPages)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	pages := make([]pageSummary, 0, 32)
	titleMap := make(map[string]uuid.UUID, 32)
	for rows.Next() {
		var (
			id    uuid.UUID
			title string
			body  string
		)
		if err := rows.Scan(&id, &title, &body); err != nil {
			return nil, nil, err
		}
		pages = append(pages, pageSummary{
			ID:    id,
			Title: title,
			Body:  truncateBody(body, cap),
		})
		// normTitle 作 key —— missing-page 后过滤 / PAGES 解析都用同一 norm。
		nt := normTitle(title)
		if nt != "" {
			titleMap[nt] = id
		}
	}
	return pages, titleMap, rows.Err()
}

// postFilter 剔除明显误报。当前规则（仿 llm_wiki lint.ts:72-79）：
// missing-page 的标题若已对应现有页 → drop（LLM 不可靠交叉引用文件列表）。
func (r *SemanticRunner) postFilter(findings []SemanticFinding, titleMap map[string]uuid.UUID) []SemanticFinding {
	if len(findings) == 0 {
		return findings
	}
	out := make([]SemanticFinding, 0, len(findings))
	for _, f := range findings {
		if normCategory(f.Category) == semanticCategoryMissingPage {
			nt := normTitle(f.Title)
			if nt != "" {
				if _, exists := titleMap[nt]; exists {
					continue // 概念已建页 → 误报，drop
				}
			}
		}
		out = append(out, f)
	}
	return out
}

// upsertFinding 写一条 review_items。返回是否新建（false = 已存在/失败）。
func (r *SemanticRunner) upsertFinding(
	ctx context.Context, projectID, ownerID uuid.UUID,
	f SemanticFinding, titleMap map[string]uuid.UUID,
) bool {
	cat := normCategory(f.Category)
	sev := normSeverity(f.Severity)
	pageIDs := resolvePageIDs(f.Pages, titleMap)
	mainID := firstUUID(pageIDs)
	dedupeKey := semanticDedupeKey(projectID, mainID, cat, f.Title+"|"+f.Detail)

	payload := map[string]any{
		"rule_family": "semantic",
		"rule_id":     "semantic:" + cat,
		"severity":    sev,
		"category":    cat,
	}
	if rt, ok := reviewTypeByCategory[cat]; ok && rt != "" {
		payload["review_type"] = rt
	}

	// S3 P0-2: contradiction 升级为独立 top-level kind（migration 00067）。
	// 其余三类 (stale / missing-page / suggestion) 仍 kind=lint —— 它们语义
	// 仍是「lint 类待办」，只有跨页矛盾才单独走审阅/处理流。dedupe_key 已含
	// category (semanticDedupeKey)，所以 backfill 升级 kind 后不重复出新条目。
	kind := KindLint
	if cat == semanticCategoryContradiction {
		kind = KindContradiction
	}

	_, created, err := r.Reviews.Upsert(ctx, UpsertInput{
		ProjectID:   projectID,
		OwnerID:     ownerID,
		Kind:        kind,
		Title:       firstNonEmpty(f.Title, "语义问题: "+cat),
		Description: f.Detail,
		PageIDs:     pageIDs,
		Payload:     payload,
		DedupeKey:   dedupeKey,
	})
	if err != nil {
		// 并发撞 UNIQUE（另一 goroutine 已写同 key）不视为致命 ——
		// review_items.dedupe_key 是 UNIQUE，SELECT-then-INSERT 窗口期
		// 撞了就当幂等成功。其它错误记 warn 继续。
		r.Logger.Warn("semantic lint: upsert finding failed",
			"dedupe_key", dedupeKey, "err", err)
		return false
	}
	return created
}

// ─── RelaySemanticCaller.Analyze ────────────────────────────────────

func (h *RelaySemanticCaller) Analyze(ctx context.Context, ownerID uuid.UUID, pages []pageSummary) ([]SemanticFinding, error) {
	if len(pages) == 0 {
		return nil, nil
	}
	if h.RelayURL == "" || h.Signer == nil {
		// 未配置 → 静默禁用（recall 契约：不报错）。
		return nil, nil
	}

	prompt := h.buildPrompt(pages)
	jwt, err := h.Signer.Sign(&bauth.Claims{UserID: ownerID.String()})
	if err != nil {
		return nil, fmt.Errorf("mint jwt: %w", err)
	}

	body := map[string]any{
		"model":      h.Model,
		"stream":     false,
		"max_tokens": 4096,
		"messages": []map[string]any{
			{"role": "user", "content": prompt},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	cctx := ctx
	if h.Timeout > 0 {
		var cancel context.CancelFunc
		cctx, cancel = context.WithTimeout(ctx, h.Timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(cctx, http.MethodPost,
		h.RelayURL+"/v1/messages", strings.NewReader(string(raw)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		buf, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("model-relay %d: %s", resp.StatusCode, string(buf))
	}

	var hubResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&hubResp); err != nil {
		return nil, fmt.Errorf("decode relay response: %w", err)
	}
	if len(hubResp.Choices) == 0 {
		return nil, fmt.Errorf("model-relay returned no choices")
	}
	return parseLintBlocks(hubResp.Choices[0].Message.Content), nil
}

// buildPrompt 渲染全项目摘要 prompt。---LINT--- 块协议对齐
// reference/llm_wiki（lint.ts:263-291），正则解析见 parseLintBlocks。
func (h *RelaySemanticCaller) buildPrompt(pages []pageSummary) string {
	cap := h.SnippetMaxChars
	if cap <= 0 {
		cap = 500
	}
	var b strings.Builder
	b.WriteString(
		"You are a wiki quality analyst. Review the wiki page summaries below " +
			"and identify genuine quality issues.\n\n" +
			"For each issue, output exactly this format and nothing else:\n\n" +
			"---LINT: type | severity | Short title---\n" +
			"Description of the issue.\n" +
			"PAGES: page1, page2\n" +
			"---END LINT---\n\n" +
			"Types:\n" +
			"- contradiction: two or more pages make conflicting claims\n" +
			"- stale: information that appears outdated or superseded\n" +
			"- missing-page: an important concept is heavily referenced but has no dedicated page\n" +
			"- suggestion: a question or source worth adding to the wiki\n" +
			"For missing-page, the Short title must be only the exact missing concept name, " +
			"without explanatory prefixes or suffixes.\n\n" +
			"Severities:\n" +
			"- warning: should be addressed\n" +
			"- info: nice to have\n\n" +
			"Only report genuine issues. Do not invent problems. " +
			"Output ONLY the ---LINT--- blocks, no other text.\n\n" +
			"## Wiki Pages\n\n",
	)
	for _, p := range pages {
		fmt.Fprintf(&b, "### %s\n%s\n\n", p.Title, truncateBody(p.Body, cap))
	}
	return b.String()
}

// lintBlockRe 解析 ---LINT: type | severity | title--- ... ---END LINT--- 块。
// 多行模式；title 取到行尾（不含 | 时不劈）。
var lintBlockRe = regexp.MustCompile(
	`(?ms)^---LINT:\s*([^|\n]+?)\s*\|\s*([^|\n]+?)\s*\|\s*([^\n]+?)\s*---\s*\n([\s\S]*?)---END LINT---`,
)

// parseLintBlocks 从 LLM 输出抽数据块 → []SemanticFinding。
// body 内：非 PAGES: 行拼成 detail；PAGES: 行拆成页标题列表。
func parseLintBlocks(content string) []SemanticFinding {
	matches := lintBlockRe.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]SemanticFinding, 0, len(matches))
	for _, m := range matches {
		rawType := strings.TrimSpace(m[1])
		rawSev := strings.TrimSpace(m[2])
		title := strings.TrimSpace(strings.Trim(m[3], "`"))
		body := strings.TrimSpace(m[4])

		var desc []string
		var pages []string
		for _, ln := range strings.Split(body, "\n") {
			t := strings.TrimSpace(ln)
			if t == "" {
				continue
			}
			if upper := strings.ToUpper(t); strings.HasPrefix(upper, "PAGES:") {
				rest := strings.TrimSpace(t[len("PAGES:"):])
				for _, p := range strings.Split(rest, ",") {
					p = strings.TrimSpace(strings.Trim(strings.TrimSpace(p), "`"))
					if p != "" {
						pages = append(pages, p)
					}
				}
				continue
			}
			desc = append(desc, t)
		}
		out = append(out, SemanticFinding{
			Category: rawType,
			Severity: rawSev,
			Title:    title,
			Detail:   strings.Join(desc, " "),
			Pages:    pages,
		})
	}
	return out
}

// ─── helpers ────────────────────────────────────────────────────────

func (r *SemanticRunner) logWarn(msg string, args ...any) {
	if r.Logger != nil {
		r.Logger.Warn(msg, args...)
	}
}

// normCategory 规范化分类。missing_page / Missing-Page 等变体归一。
func normCategory(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "_", "-")
	switch s {
	case semanticCategoryContradiction, semanticCategoryStale,
		semanticCategoryMissingPage, semanticCategorySuggestion:
		return s
	}
	return s // 非标准分类原样保留（rule_id 用原值，徽章留空）
}

func normSeverity(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s != "warning" && s != "info" {
		return "info"
	}
	return s
}

// normTitle 标题归一：lower + 折叠空白 + trim。供 titleMap 去重匹配。
// 轻量实现（未引入 x/text/NFKC）—— 对中英文标题匹配足够。
func normTitle(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}

// resolvePageIDs 把 finding.Pages 标题经 normTitle 查 titleMap → uuid 列表（去重）。
func resolvePageIDs(titles []string, titleMap map[string]uuid.UUID) []uuid.UUID {
	if len(titles) == 0 {
		return nil
	}
	seen := make(map[uuid.UUID]struct{}, len(titles))
	out := make([]uuid.UUID, 0, len(titles))
	for _, t := range titles {
		nt := normTitle(t)
		if nt == "" {
			continue
		}
		id, ok := titleMap[nt]
		if !ok {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func firstUUID(ids []uuid.UUID) uuid.UUID {
	if len(ids) == 0 {
		return uuid.Nil
	}
	return ids[0]
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// semanticDedupeKey 规范 dedupe_key。有主 page → semantic:<page>:<cat>:<hash>；
// 无（missing-page 无现页 / PAGES 解析失败）→ semantic:project:<pid>:<cat>:<hash>。
// 同输入幂等：重跑不重复写（review_items.dedupe_key UNIQUE）。
func semanticDedupeKey(projectID, mainID uuid.UUID, cat, discrim string) string {
	h := hashSubKey(cat + "|" + discrim)
	if mainID != uuid.Nil {
		return "semantic:" + mainID.String() + ":" + cat + ":" + h
	}
	return "semantic:project:" + projectID.String() + ":" + cat + ":" + h
}

// truncateBody 截 body 到 ≤ n 字节（按 rune 计更准，但字节截断对 LLM
// 摘要足够且省一次 rune 扫描）。
func truncateBody(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
