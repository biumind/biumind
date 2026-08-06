# BiuMind SDK Protocol v1 — Dart bindings

Auto-generated via `json_serializable` + `build_runner`. 字段名严格对齐服务端（snake_case 与 camelCase 混用，跟服务端一致）。

## 布局

```
v1/
├── common.dart           # AnthropicMessage / ModelUsage 共享类型
├── data/
│   ├── sdk_message.dart  # SDKMessage 抽象基类（type/uuid/sessionId）
│   ├── user.dart         # SDKUserMessage（含 isReplay 字段表示 replay）
│   ├── assistant.dart    # SDKAssistantMessage / SDKPartialAssistantMessage
│   ├── tool.dart         # SDKToolProgress / SDKToolUseSummary
│   ├── result.dart       # SDKResultSuccess / SDKResultError
│   ├── system.dart       # 16 个 system subtype（含 auth_status / rate_limit_event 等）
│   ├── streamlined.dart
│   └── post_turn.dart
├── control/              # 21 ControlRequest（按 schema 1:1 拆 14 文件）+ wrappers.dart
├── hooks/                # 27 HookEvent + index.dart（HookInputFactory dispatcher）
├── permissions.dart      # 6 PermissionUpdate variant + PermissionResult
├── mcp.dart              # 5 McpServerConfig + Status
├── agents.dart           # SlashCommand / AgentInfo / AgentDefinition
├── settings.dart         # GetSettingsResponse
├── lifecycle.dart        # 6 BiuMind 自有 lifecycle 帧
├── biumind_ext.dart      # CreateSessionReq / Session / EnvironmentInfo + Mode 常量
└── service.dart          # ServiceFrame.fromJson —— 顶层 dispatcher（peek type → 具体类型）
```

## 用法

### 解析收到的 WS 帧

```dart
import 'package:biumind/data/api/sdkproto/v1/service.dart';
import 'package:biumind/data/api/sdkproto/v1/data/user.dart';

final frame = ServiceFrame.fromJson(jsonDecode(rawJson));

switch (frame) {
  case SDKUserMessage um:
    print('user said: ${um.message.content}');
  case SDKControlRequest req when req.subtype == 'can_use_tool':
    askPermission(req);
  case Lifecycle lc:
    handleLifecycle(lc);
  // ...
}
```

### 发送帧

```dart
final msg = SDKUserMessage(
  message: AnthropicMessage(role: 'user', content: 'hello'),
  uuid: uuidV4(),
  sessionId: session.sessionId,
);
ws.send(jsonEncode(msg.toJson()));
```

### Hook input dispatcher

```dart
import 'package:biumind/data/api/sdkproto/v1/hooks/index.dart';

final hookInput = HookInputFactory.fromJson(payload);
// is PreToolUse / is PostToolUse / ...
```

## 命名冲突约定

部分 schema 类型名跟 BiuMind 内部类型撞名，统一加 `Hook` / `Update` 后缀解决：

- `PermissionRequest` (control) vs `PermissionRequestHook` (hooks)
- `Elicitation` (control) vs `ElicitationHook` (hooks)
- `TaskCreated` (hooks) vs `SDKTaskStarted` (data)
- `SetMode` (n/a) vs `SetModeUpdate` (permissions PermissionUpdate variant)
- `Notification` (system data) vs `NotificationHook` (hooks)
- `ElicitationResult` 在两边都有，hook 端用 `ElicitationResultHook`
- `SetPermissionMode` (control set_permission_mode) vs `SetModeUpdate` (permissions setMode update)

跟 schema/Go 端命名规则保持一致。

## 当前状态

S1-5 完成 —— 80+ class 全量 codegen，41 个 `.g.dart` 由 `build_runner` 生成。

**全量 codegen 跑命令**：

```bash
cd apps/client
flutter pub run build_runner build --delete-conflicting-outputs
```
