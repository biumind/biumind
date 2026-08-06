// Adapter — bridges *Client to rss.WikiSink. Builds the page +
// blocks: 1) AI takeaway block, 2) source link, 3) full content.

package wiki

import (
	"context"
	"strings"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss"
)

type SDKAdapter struct {
	Client *Client
}

var _ rss.WikiSink = (*SDKAdapter)(nil)

func (a *SDKAdapter) Sink(ctx context.Context, token string, in rss.WikiSinkInput) (*rss.WikiSinkResult, error) {
	proj, err := a.Client.EnsureProject(ctx, token)
	if err != nil {
		return nil, err
	}
	page, err := a.Client.CreatePage(ctx, token, proj.ID, in.Title)
	if err != nil {
		return nil, err
	}

	pos := 1.0
	// Block 1 — AI takeaway + bullets (when present).
	if in.AITakeaway != "" {
		md := "## AI 摘要\n\n> " + in.AITakeaway + "\n\n"
		for _, b := range in.AIBullets {
			md += "- " + b + "\n"
		}
		_, _ = a.Client.CreateBlock(ctx, token, proj.ID, page.ID, pos,
			"markdown", map[string]any{"markdown": md})
		pos += 1.0
	}
	// Block 2 — source metadata.
	srcLines := []string{"## 来源"}
	if in.FeedTitle != "" {
		srcLines = append(srcLines, "**站点**: "+in.FeedTitle)
	}
	if in.Author != "" {
		srcLines = append(srcLines, "**作者**: "+in.Author)
	}
	if in.URL != "" {
		srcLines = append(srcLines, "**链接**: ["+in.URL+"]("+in.URL+")")
	}
	_, _ = a.Client.CreateBlock(ctx, token, proj.ID, page.ID, pos,
		"markdown", map[string]any{"markdown": strings.Join(srcLines, "\n\n")})
	pos += 1.0

	// Block 3 — full content (HTML preserved as-is; brain wiki
	// renderer handles HTML via the markdown HTML extension).
	if in.ContentHTML != "" {
		_, _ = a.Client.CreateBlock(ctx, token, proj.ID, page.ID, pos,
			"markdown", map[string]any{
				"markdown": "## 正文\n\n" + in.ContentHTML,
			})
	}

	// Suggested tags — for v0 use ai_topics directly. AI-driven
	// tag refinement against user's wiki tag corpus = v3.
	suggested := append([]string{}, in.AITopics...)

	return &rss.WikiSinkResult{
		PageID:        page.ID,
		ProjectID:     proj.ID,
		SuggestedTags: suggested,
	}, nil
}
