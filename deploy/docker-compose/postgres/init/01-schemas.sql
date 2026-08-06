-- 每个服务一个 schema；service migration 自己管表结构
-- 这里只建空 schema + 给 service 用户授权
CREATE SCHEMA IF NOT EXISTS identity;
CREATE SCHEMA IF NOT EXISTS hub;
CREATE SCHEMA IF NOT EXISTS brain;
CREATE SCHEMA IF NOT EXISTS runtime;
CREATE SCHEMA IF NOT EXISTS sandbox;
CREATE SCHEMA IF NOT EXISTS presence;
CREATE SCHEMA IF NOT EXISTS billing;
CREATE SCHEMA IF NOT EXISTS audit;
CREATE SCHEMA IF NOT EXISTS deploy;

COMMENT ON SCHEMA identity IS 'Users, orgs, teams, roles, OIDC';
COMMENT ON SCHEMA hub      IS 'Provider keys (BYOK), virtual keys, usage';
COMMENT ON SCHEMA brain    IS 'Wiki pages/blocks, graph nodes/edges, memory, search';
COMMENT ON SCHEMA runtime  IS 'Sessions, tasks, skills, scheduled tasks';
COMMENT ON SCHEMA sandbox  IS 'Sandbox lifecycle records, snapshots';
COMMENT ON SCHEMA presence IS 'Device tracking';
COMMENT ON SCHEMA billing  IS 'Subscriptions, invoices, budgets';
COMMENT ON SCHEMA audit    IS 'Immutable audit log; admin-only';
COMMENT ON SCHEMA deploy   IS 'One-click deployment records';
