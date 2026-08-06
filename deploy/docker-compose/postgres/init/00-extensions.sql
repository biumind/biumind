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
BEGIN
  IF EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'zhparser') THEN
    CREATE EXTENSION IF NOT EXISTS zhparser;
    IF NOT EXISTS (SELECT 1 FROM pg_ts_config WHERE cfgname = 'biumind_zhcn') THEN
      CREATE TEXT SEARCH CONFIGURATION biumind_zhcn (PARSER = zhparser);
      ALTER TEXT SEARCH CONFIGURATION biumind_zhcn
        ADD MAPPING FOR n,v,a,i,e,l,j,nz WITH simple;
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
