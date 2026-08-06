// SDKMessage union — 数据平面 28 个 variant 的共同祖先。
//
// 用 Dart 3 的 sealed class，子类各自定义；
// fromJson 工厂在 service.dart 那一层做（peek type + subtype 后 dispatch）。
//
// 这里 SDKMessage 只是标记 base class 提供 type/uuid/session_id 三个共享字段，
// 子类按各自字段表加自己的字段。
//
// 注意：data/system.dart 等子类文件 import 此文件，但 SDKMessage 自己不需要
// import 子类（dispatch 在 service.dart 里）。

abstract class SDKMessage {
  /// Variant discriminator — "user" / "assistant" / "result" / "system" / ...
  String get type;

  /// 当前 variant 的 uuid（协议规约：所有 SDKMessage 都带 uuid + session_id）。
  String get uuid;

  /// session id 字符串。
  String get sessionId;
}
