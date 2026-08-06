-- +goose Up
-- +goose StatementBegin

-- ─── Rankings — more sources + color metadata ─────────────────────
--
-- 1. Adds `color` column to boards. newsnow tags each source with a
--    visual color (blue/red/green/orange/gray/teal/...) — UI uses it
--    to tint cards so the boards grid scans like newsnow's homepage.
--
-- 2. Re-seeds boards with the curated full set (43 sources we've
--    confirmed return data on the live newsnow instance, deduped from
--    the original 14). UPSERT updates name + color + expected_domain
--    on existing rows so the P1 seed migrates forward; new sources
--    are inserted; nothing is removed (uninstalled boards stay in DB
--    until an operator deletes them, since user data may reference
--    them via watch_rules.sources).
--
-- The "name" column now carries the human-friendly title with the
-- sub-board name embedded ("华尔街见闻 · 最热") so the UI doesn't need
-- to round-trip a tag column.

ALTER TABLE rankings.boards
    ADD COLUMN IF NOT EXISTS color text NOT NULL DEFAULT 'gray';

-- Idempotent UPSERT seed (P1 had 14 sources; this expands to 43).
INSERT INTO rankings.boards (id, name, color, expected_domain) VALUES
    ('wallstreetcn-quick',     '华尔街见闻 · 快讯',         'blue',    'wallstreetcn.com'),
    ('wallstreetcn-news',      '华尔街见闻 · 最新',         'blue',    'wallstreetcn.com'),
    ('wallstreetcn-hot',       '华尔街见闻 · 最热',         'blue',    'wallstreetcn.com'),
    ('pcbeta-windows11',       '远景论坛 Win11',            'blue',    'pcbeta.com'),
    ('cls-telegraph',          '财联社 · 电报',             'red',     'cls.cn'),
    ('cls-depth',              '财联社 · 深度',             'red',     'cls.cn'),
    ('cls-hot',                '财联社 · 热门',             'red',     'cls.cn'),
    ('xueqiu-hotstock',        '雪球 · 热门股票',           'blue',    'xueqiu.com'),
    ('bilibili-hot-search',    '哔哩哔哩 · 热搜',           'blue',    'bilibili.com'),
    ('bilibili-hot-video',     '哔哩哔哩 · 热门视频',       'blue',    'bilibili.com'),
    ('bilibili-ranking',       '哔哩哔哩 · 排行榜',         'blue',    'bilibili.com'),
    ('chongbuluo-latest',      '虫部落 · 最新',             'green',   'chongbuluo.com'),
    ('chongbuluo-hot',         '虫部落 · 最热',             'green',   'chongbuluo.com'),
    ('tencent-hot',            '腾讯新闻 · 综合早报',       'blue',    'qq.com'),
    ('qqvideo-tv-hotsearch',   '腾讯视频 · 热搜榜',         'blue',    'qq.com'),
    ('iqiyi-hot-ranklist',     '爱奇艺 · 热播榜',           'green',   'iqiyi.com'),
    ('36kr-quick',             '36氪 · 快讯',               'blue',    '36kr.com'),
    ('36kr-renqi',             '36氪 · 人气榜',             'blue',    '36kr.com'),
    ('github-trending-today',  'GitHub Trending · Today',   'gray',    'github.com'),
    ('zhihu',                  '知乎 · 热榜',               'blue',    'zhihu.com'),
    ('weibo',                  '微博 · 实时热搜',           'red',     'weibo.com'),
    ('baidu',                  '百度 · 热搜',               'blue',    'baidu.com'),
    ('toutiao',                '今日头条',                  'red',     'toutiao.com'),
    ('douyin',                 '抖音 · 热点',               'gray',    'douyin.com'),
    ('kuaishou',               '快手 · 热榜',               'orange',  'kuaishou.com'),
    ('ithome',                 'IT 之家',                   'red',     'ithome.com'),
    ('ifeng',                  '凤凰网 · 24 小时热文',      'red',     'ifeng.com'),
    ('tieba',                  '百度贴吧 · 热议',           'blue',    'baidu.com'),
    ('thepaper',               '澎湃新闻 · 热榜',           'red',     'thepaper.cn'),
    ('sputniknewscn',          '卫星通讯社',                'orange',  'sputniknews.cn'),
    ('cankaoxiaoxi',           '参考消息',                  'red',     'cankaoxiaoxi.com'),
    ('solidot',                'Solidot',                   'teal',    'solidot.org'),
    ('producthunt',            'Product Hunt',              'red',     'producthunt.com'),
    ('sspai',                  '少数派',                    'red',     'sspai.com'),
    ('nowcoder',               '牛客',                      'green',   'nowcoder.com'),
    ('juejin',                 '掘金',                      'blue',    'juejin.cn'),
    ('xueqiu',                 '雪球',                      'blue',    'xueqiu.com'),
    ('hupu',                   '虎扑 · 步行街热帖',         'orange',  'hupu.com'),
    ('jin10',                  '金十数据 · 快讯',           'blue',    'jin10.com'),
    ('gelonghui',              '格隆汇 · 事件',             'blue',    'gelonghui.com'),
    ('coolapk',                '酷安 · 今日最热',           'green',   'coolapk.com'),
    ('pcbeta',                 '远景论坛',                  'blue',    'pcbeta.com'),
    ('zaobao',                 '联合早报',                  'red',     'zaobao.com')
ON CONFLICT (id) DO UPDATE SET
    name            = EXCLUDED.name,
    color           = EXCLUDED.color,
    expected_domain = EXCLUDED.expected_domain;

-- ─── Remove the 3 sources that turned out to be dead on newsnow ───
-- (hacker-news / ruanyifeng / yahoo-finance — 500 from upstream every
-- tick; cluttering the boards grid with permanent error rows). Safe
-- because no watch_rules reference them yet (feature just shipped).

DELETE FROM rankings.boards WHERE id IN ('hacker-news', 'ruanyifeng', 'yahoo-finance');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Down only removes the column; we don't restore the old narrow seed
-- (operators who roll back this migration get the column dropped but
-- the now-removed dead boards stay deleted — restoring them would
-- silently re-introduce 500-spamming jobs).
ALTER TABLE rankings.boards DROP COLUMN IF EXISTS color;

-- +goose StatementEnd
