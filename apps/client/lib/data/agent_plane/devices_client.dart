// DevicesClient —— Flutter 端调 brain 的设备配对 + device token 管理 endpoints
// (Runtime v3 R6.1 / D5)。用户在已登录设备上：批准新设备配对码、列出/吊销
// 已配对设备。
//
// 路由对齐 services/brain/internal/agentplane/device_api.go：
//   POST   /v1/agent/devices/pair/approve   批准配对码（绑定到当前用户）
//   GET    /v1/agent/devices                列出当前用户的 device token
//   DELETE /v1/agent/devices/{id}           吊销一台设备

import 'package:flutter/foundation.dart' show debugPrint;

import '../api/_http_helpers.dart';

/// 一台已配对设备（device token 元数据，不含 token 明文）。
/// 设备工具权限 preset（R6.3 / D7，与 brain migration 00043 + daemon floor.go 一致）。
const kToolPolicies = ['readonly', 'workspace-write', 'full'];

class PairedDevice {
  final String deviceId;
  final String name;
  final String prefix;

  /// 该设备可远程调用的工具范围 preset：readonly | workspace-write | full。
  final String toolPolicy;

  /// R6.4：该设备是否在线（最新 environment state=='online'）+ 最近活跃时间。
  final bool online;
  final DateTime? lastSeen;
  final DateTime? createdAt;
  final DateTime? lastUsedAt;
  final DateTime? expiresAt;
  final bool revoked;

  PairedDevice({
    required this.deviceId,
    required this.name,
    required this.prefix,
    this.toolPolicy = 'workspace-write',
    this.online = false,
    this.lastSeen,
    this.createdAt,
    this.lastUsedAt,
    this.expiresAt,
    this.revoked = false,
  });

  static DateTime? _ts(dynamic v) =>
      v is String ? DateTime.tryParse(v) : null;

  factory PairedDevice.fromJson(Map<String, dynamic> j) => PairedDevice(
        deviceId: j['device_id'] as String,
        name: j['name'] as String? ?? '',
        prefix: j['prefix'] as String? ?? '',
        toolPolicy: j['tool_policy'] as String? ?? 'workspace-write',
        online: j['online'] as bool? ?? false,
        lastSeen: _ts(j['last_seen_at']),
        createdAt: _ts(j['created_at']),
        lastUsedAt: _ts(j['last_used_at']),
        expiresAt: _ts(j['expires_at']),
        revoked: j['revoked'] as bool? ?? false,
      );
}

class DevicesClient {
  /// brain HTTP base URL，末尾不带 `/`。
  final String baseUrl;
  final Future<String?> Function() tokenProvider;
  final AuthErrorHandler? onAuthError;

  DevicesClient({
    required this.baseUrl,
    required this.tokenProvider,
    this.onAuthError,
  });

  /// 批准一个配对码（绑定到当前登录用户）。返回设备机器名供确认。
  /// 错码 / 过期 → 抛 ApiError(404)。
  Future<String> approvePairing(String code) async {
    final tok = await tokenProvider();
    final json = await apiRequest(
      method: 'POST',
      url: Uri.parse('$baseUrl/v1/agent/devices/pair/approve'),
      bearerToken: tok,
      body: {'code': code},
      onAuthError: onAuthError,
    );
    final machine = json['machine_name'] as String? ?? '';
    debugPrint('[devices] approved pairing machine=$machine');
    return machine;
  }

  /// 列出当前用户已配对的设备。
  Future<List<PairedDevice>> listDevices() async {
    final tok = await tokenProvider();
    final json = await apiRequest(
      method: 'GET',
      url: Uri.parse('$baseUrl/v1/agent/devices'),
      bearerToken: tok,
      onAuthError: onAuthError,
    );
    final list = json['devices'] as List?;
    if (list == null) return const [];
    return list
        .cast<Map<String, dynamic>>()
        .map(PairedDevice.fromJson)
        .toList();
  }

  /// 改一台设备的工具权限 preset（readonly | workspace-write | full）。
  /// brain 据此在 createSession 时 stamp 进 work payload；daemon 取交集做地板。
  Future<void> setDevicePolicy(String deviceId, String toolPolicy) async {
    final tok = await tokenProvider();
    await apiRequest(
      method: 'PATCH',
      url: Uri.parse('$baseUrl/v1/agent/devices/$deviceId'),
      bearerToken: tok,
      body: {'tool_policy': toolPolicy},
      onAuthError: onAuthError,
    );
    debugPrint('[devices] set policy device=$deviceId policy=$toolPolicy');
  }

  /// 吊销一台设备的 device token。之后该设备的请求一律 401。
  Future<void> revokeDevice(String deviceId) async {
    final tok = await tokenProvider();
    await apiRequest(
      method: 'DELETE',
      url: Uri.parse('$baseUrl/v1/agent/devices/$deviceId'),
      bearerToken: tok,
      onAuthError: onAuthError,
    );
    debugPrint('[devices] revoked device=$deviceId');
  }
}
