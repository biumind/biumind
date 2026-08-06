-- +goose Up
-- §⑤ Milkdown Path C：body_md 权威列。
-- pages 由「blocks 权威」过渡到「body_md 权威 + blocks 派生投影」：
--   * PUT body_md（Milkdown 编辑器整篇写）事务内 mdparse.ParseBlocks 重算 blocks
--     （reconcileBlocksTx 按 type+content greedy 匹配保 block_id 连续，不 dangle
--     graph_edges / wiki_chunks / page_revisions 的 block_id 引用）。
--   * blocks 仍作 chunks/embed/graph/vision/reviews 的下游源（4 锚点不动，留 Path A 收尾）。
-- 回填由 Go 端 Store.BackfillBodyMd() 启动幂等执行（body_md='' 且有 live blocks 的页
-- 按 BlocksToMarkdown 重算）—— SQL 无法聚合 blocks content → markdown。
ALTER TABLE brain.pages ADD COLUMN body_md text NOT NULL DEFAULT '';
-- page_revisions 同步加 body_md：snapshot 存写前 body_md 原文，restore 无损恢复。
-- blocks_json 仍是 block 投影快照（④），但 body_md 才是权威原文 —— 否则 PUT body_md 后
-- restore 会用 blocks_json 投影回填 body_md，丢嵌套/格式（list 折叠、code fence 细节）。
ALTER TABLE brain.page_revisions ADD COLUMN body_md text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE brain.page_revisions DROP COLUMN body_md;
ALTER TABLE brain.pages DROP COLUMN body_md;
