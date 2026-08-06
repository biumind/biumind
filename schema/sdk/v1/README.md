# BiuMind SDK Protocol v1 — JSON Schema

设计与字段映射：见 `docs/BiuMind-Agent-Plane-Schema-Mapping.md`（命名约定、union discriminator 规范）。

## 布局

```
v1/
├── index.json          # 顶层 SDKMessage / SDKControlRequest / StdinMessage 等入口
├── _common.json        # 共享类型（UUID / 时间戳 / RoleEnum / ModelUsage）
├── data/               # 24+ SDKMessage variant
├── control/            # 21 ControlRequest + 9 Response（wrappers.json 是壳）
├── hooks/              # 27 HookEvent + HookInput union
├── permissions.json    # PermissionUpdate / Behavior / Mode / Result
├── mcp.json            # 5 McpServerConfig 类型 + Status
├── agents.json         # SlashCommand / AgentInfo / AgentDefinition / ModelInfo
├── settings.json       # GetSettingsResponse 嵌套 + SettingSource enum
├── lifecycle.json      # BiuMind 自有：KeepAlive / SessionDesynced / ...
├── biumind_ext.json    # CreateSessionReq / Session / Mode enum
├── service.json        # StdinMessage / StdoutMessage 顶层 oneof
└── fixtures/           # 实例样本（CI 自动校验）
```

## 校验

```bash
make schema-validate
```

工具：`tools/schema-validate/main.go`，用 `santhosh-tekuri/jsonschema/v6` 编译 + 校验。

## Fixture 约定

每个 fixture 文件含 `"$schema"` 字段，必须指向**具体 `$defs/X`**而不是文件根 —— 因为我们的 schema 文件顶层只放 `$defs`（无 `type` / `required` 等约束），仅指向文件根会让校验变成 vacuous true：

```json
{
  "$schema": "data/user.json#/$defs/SDKUserMessage",
  "type": "user",
  "message": { "role": "user", "content": "hi" },
  "uuid": "u1",
  "session_id": "s1"
}
```

校验工具读 `$schema` 字段决定用哪个 schema 校验该 fixture。

## 当前状态

- **S1-1** ✅ 建 46 个 schema 文件骨架 + 校验工具链
- **S1-2** ✅ 数据平面：`_common.json` 共享类型 + `data/*.json` 8 个文件覆盖 28 个 SDKMessage variant + Go struct (`packages/go-sdk/biu/sdkproto/v1/`) + 27 个 round-trip 单测 + 8 个 fixture
- **S1-3** ✅ 控制平面：`control/*.json` 14 个文件覆盖 21 ControlRequest + wrappers + Go struct + 21+5 round-trip 单测 + 6 个 fixture
- **S1-4** ✅ Hook + MCP + Permissions + Agents + Settings：14 hook 文件（27 variant + union）+ permissions/mcp/agents/settings 4 个顶层文件 + 5 个 Go 文件 + 43 个新测 + 8 个新 fixture
- **S1-5** ✅ Lifecycle + 顶层 union + Dart 全量 codegen：lifecycle.json（6 BiuMind 自有帧）+ biumind_ext.json + service.json + Go 3 文件 + **Dart 45 源文件 + 41 .g.dart**（80+ class）+ TS 占位 + Go E2E 9 帧 session 测试 = **S1 协议层全部完成**

## S1 完成总览

| 度量 | 数 |
|---|---|
| Schema 文件 | 46 个（全部填充非空 `$defs`） |
| Go 文件 | 11 个（`_common` / `data` / `unmarshal` / `control` / `wrappers` / `hooks` / `permissions` / `mcp` / `agents` / `settings` / `lifecycle` / `biumind_ext` / `service`） |
| Go 测试 | **105 个** pass |
| Dart 文件 | 45 源文件 + 41 自动生成 |
| Dart 测试 | 9 个 pass |
| Fixture | 22 个 |
| 总工日 | 6.5（按 Dev Plan 估算） |
