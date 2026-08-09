-- 启用 BiuMind 需要的所有扩展
-- 测试环境一次性装齐；生产用 RDS extensions allowlist
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";       -- gen_random_uuid + crypto
CREATE EXTENSION IF NOT EXISTS "vector";          -- pgvector
CREATE EXTENSION IF NOT EXISTS "pg_trgm";         -- 模糊搜索
CREATE EXTENSION IF NOT EXISTS "btree_gin";
CREATE EXTENSION IF NOT EXISTS "btree_gist";
CREATE EXTENSION IF NOT EXISTS "citext";          -- 大小写不敏感字段（email）
CREATE EXTENSION IF NOT EXISTS "ltree";           -- 树形数据（pages 层级）

-- 中文分词：zhparser（可选 — QUICKSTART 用 stock pgvector 时不可用）
-- 不可用时 biumind_zhcn 退化为 simple 配置（按空格分词，对中文混排不理想，
-- 但保持 SQL 不破坏）。生产部署用自家 Dockerfile 装 zhparser。
DO $ext$
DECLARE
  tok text;
BEGIN
  IF EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'zhparser') THEN
    CREATE EXTENSION IF NOT EXISTS zhparser;
    IF NOT EXISTS (SELECT 1 FROM pg_ts_config WHERE cfgname = 'biumind_zhcn') THEN
      CREATE TEXT SEARCH CONFIGURATION biumind_zhcn (PARSER = zhparser);
      -- 动态映射：不同发行版（阿里云 RDS / 自建 / 旧 SCWS）的 zhparser
      -- 暴露的 token 词性表被裁剪程度不一，硬编码列表（如 n,v,a,...,nz）
      -- 会在缺词性的版本上报 SQLSTATE 22023 "token type X does not exist"。
      -- 逐个仅映射 ts_token_type 实际返回的 token，任意版本自适应。
      FOR tok IN SELECT alias FROM ts_token_type('zhparser') LOOP
        EXECUTE format('ALTER TEXT SEARCH CONFIGURATION biumind_zhcn ADD MAPPING FOR %I WITH simple', tok);
      END LOOP;
    END IF;
  ELSE
    -- Fallback: alias biumind_zhcn to the built-in 'simple' config so
    -- search migrations using to_tsvector('biumind_zhcn', …) keep working.
    IF NOT EXISTS (SELECT 1 FROM pg_ts_config WHERE cfgname = 'biumind_zhcn') THEN
      CREATE TEXT SEARCH CONFIGURATION biumind_zhcn (COPY = simple);
    END IF;
  END IF;
END
$ext$;
