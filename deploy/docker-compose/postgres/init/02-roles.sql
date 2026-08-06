-- 测试环境用同一个 superuser 简化；生产应每服务独立角色 + RLS
-- 这里只声明意图，确保 default privilege 设置好
ALTER DEFAULT PRIVILEGES IN SCHEMA identity, hub, brain, runtime, sandbox, presence, billing, deploy
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO biumind;

ALTER DEFAULT PRIVILEGES IN SCHEMA identity, hub, brain, runtime, sandbox, presence, billing, deploy
  GRANT USAGE, SELECT ON SEQUENCES TO biumind;

-- audit 只读 + 仅 INSERT（用 trigger 拦改写）
ALTER DEFAULT PRIVILEGES IN SCHEMA audit
  GRANT SELECT, INSERT ON TABLES TO biumind;
