-- +goose Up
-- +goose StatementBegin

-- ─── M6: starter packs (主题包) ──────────────────────────────────
--
-- Curated bundles of feed URLs the discover tab offers as 1-click
-- onboarding. Static-ish: maintained via migration so updates ship
-- with the binary, not via admin UI (v3 candidate).

CREATE TABLE IF NOT EXISTS rss.starter_packs (
    id          text PRIMARY KEY,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    icon_emoji  text NOT NULL DEFAULT '',
    feeds       jsonb NOT NULL,
    sort_order  int NOT NULL DEFAULT 100,
    created_at  timestamptz NOT NULL DEFAULT now()
);

INSERT INTO rss.starter_packs (id, name, description, icon_emoji, feeds, sort_order) VALUES
    ('ai',        'AI / 大模型',
     '前沿 LLM / 多模态 / Agent 研究与产品动态',
     '🤖',
     '[
        {"url":"https://www.anthropic.com/news/rss.xml","title":"Anthropic Blog"},
        {"url":"https://openai.com/blog/rss.xml","title":"OpenAI Blog"},
        {"url":"https://blog.google/technology/ai/rss/","title":"Google AI Blog"},
        {"url":"https://huggingface.co/blog/feed.xml","title":"Hugging Face"},
        {"url":"https://simonwillison.net/atom/everything/","title":"Simon Willison"}
      ]'::jsonb,
     1),
    ('finance',   '投资 / 财经',
     '宏观 / 个股 / 一级市场 / 风口资讯',
     '💹',
     '[
        {"url":"https://wallstreetcn.com/rss/","title":"华尔街见闻"},
        {"url":"https://www.cls.cn/rss","title":"财联社"},
        {"url":"https://36kr.com/feed","title":"36氪"}
      ]'::jsonb,
     2),
    ('design',    '设计 / 产品',
     '产品打磨 / 设计趋势 / 用户体验',
     '🎨',
     '[
        {"url":"https://uxdesign.cc/feed","title":"UX Collective"},
        {"url":"https://www.nngroup.com/feed/rss/","title":"NN/g"},
        {"url":"https://sspai.com/feed","title":"少数派"}
      ]'::jsonb,
     3),
    ('tech',      '科技 / 工程',
     '工程师必看的科技深度文章',
     '⚙️',
     '[
        {"url":"https://hnrss.org/frontpage","title":"Hacker News"},
        {"url":"https://www.solidot.org/index.rss","title":"Solidot"},
        {"url":"https://www.ithome.com/rss/","title":"IT之家"},
        {"url":"https://feeds.feedburner.com/ruanyifeng","title":"阮一峰的网络日志"}
      ]'::jsonb,
     4),
    ('chinese',   '中文资讯',
     '聚合中文圈热门内容与时评',
     '🇨🇳',
     '[
        {"url":"https://www.thepaper.cn/rss_chosen.jsp","title":"澎湃新闻"},
        {"url":"https://www.zhihu.com/rss","title":"知乎日报"},
        {"url":"https://www.juejin.cn/rss","title":"掘金"}
      ]'::jsonb,
     5)
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name,
    description = EXCLUDED.description,
    icon_emoji = EXCLUDED.icon_emoji,
    feeds = EXCLUDED.feeds,
    sort_order = EXCLUDED.sort_order;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS rss.starter_packs;

-- +goose StatementEnd
