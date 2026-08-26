// Platform capability detection.
//
// The BiuMind client runs on six surfaces (macOS / iOS / Android / Linux
// / Windows / Web) which each have a different set of OS primitives
// available. Rather than scatter `if (kIsWeb)` checks across every
// feature module, we centralise the rules here. Features consult
// [PlatformCaps] and either branch on a flag or refuse to expose UI
// when the cap isn't present.
//
// Today this is mostly documentation — the existing features only need
// `hasLocalPty=false` on web to know that future tool-call features
// (P5.3.5 PTY bridge) must route through the Sandbox service instead of
// running in-process. As more features land that need OS primitives,
// add the relevant flag here, never inline.

import 'package:flutter/foundation.dart'
    show kIsWeb, defaultTargetPlatform, TargetPlatform;
import 'package:flutter_riverpod/flutter_riverpod.dart';

class PlatformCaps {
  /// True on macOS / Linux / Windows / Android / iOS — anywhere we can
  /// spawn a child process and pipe a PTY.
  ///
  /// Web is always false: tool calls that would have spawned a local
  /// shell must be forwarded through the Sandbox service (P5.1) instead.
  final bool hasLocalPty;

  /// True when the platform exposes a real filesystem the user can
  /// browse (macOS / Linux / Windows). iOS / Android sandboxes count as
  /// false since the Files API is permission-gated. Web is false.
  final bool hasFileSystem;

  /// True when the platform supports OS-level notifications. Web has
  /// the Notification API but we don't wire it yet — flagged false until
  /// a permission-prompt UX exists.
  final bool hasNotifications;

  /// True when the platform supports background isolates / workers for
  /// off-thread compute. All native platforms + Web (via web workers).
  final bool supportsBackgroundIsolates;

  /// True when the platform allows persistent local SQLite via
  /// `dart:ffi`. Web today is false — we use sql.js in-memory until
  /// P6.3.5 wires up persistent WASM + IndexedDB.
  final bool hasPersistentSqlite;

  /// True when webview_flutter has a working embedded engine on this
  /// platform (macOS / iOS / Android per pubspec's wkwebview + android
  /// implementations). Windows / Linux / Web are false — calling
  /// `WebViewController()` there throws MissingPluginException, so UI
  /// must branch on this cap and render the external-browser fallback
  /// instead (M1.14, Repo Apps).
  final bool hasEmbeddedWebView;

  /// True when the local repo-app runner (`biu repo-app ensure`) is
  /// supported: macOS / Linux. Windows is false (PID/signal code in the
  /// CLI is Unix-only, TechPlan §3.3); mobile / web false. Gates the
  /// "安装 GitHub 应用" entry and the repo window page.
  final bool hasRepoAppRunner;

  /// True on iOS / Android — 移动端平台习惯（长按系统 callout、触控热区
  /// 等）与桌面/Web 不同的场景据此分流。编辑器右键菜单即一例：移动端
  /// 维持系统菜单（features.contextMenu = 'native'），桌面/Web 用 bundle
  /// 自绘菜单（'custom'）。
  final bool isMobile;

  /// True when the platform pasteboard supports multiple representations
  /// (text + html) — 目前只有 macOS（NSPasteboard，经 biumind/clipboard
  /// method channel 写）。编辑器「复制」双格式据此分流：true 走 channel，
  /// false 回退 Clipboard.setData 纯文本。
  final bool hasRichClipboard;

  /// 编辑器 bundle 的平台标记（init features.platform，M1 移动端）：
  /// 'ios' | 'android' | 'macos' | 'web'。bundle 据此分流 CSS/动画/移动端
  /// 裁剪。未发行的桌面平台（Windows/Linux）回落 'web'（非移动端行为）。
  final String editorPlatform;

  /// True when the platform has a usable OS 级系统分享面板（share_plus）：
  /// iOS / Android 原生分享 sheet、macOS 分享 picker、Web navigator.share。
  /// Windows / Linux 的分享 UX 不确定，关闭入口（复制链接仍可用）。
  /// 笔记分享弹层的「系统分享…」按钮据此显隐（S2）。
  final bool hasSystemShare;

  const PlatformCaps({
    required this.hasLocalPty,
    required this.hasFileSystem,
    required this.hasNotifications,
    required this.supportsBackgroundIsolates,
    required this.hasPersistentSqlite,
    required this.hasEmbeddedWebView,
    required this.hasRepoAppRunner,
    this.isMobile = false,
    this.hasRichClipboard = false,
    this.editorPlatform = 'web',
    this.hasSystemShare = false,
  });

  factory PlatformCaps.detect() {
    if (kIsWeb) {
      // Persistent sqlite via drift's WasmDatabase (OPFS / IndexedDB)
      // landed in the WASM hardening pass; the cap reports best-case so
      // the UI doesn't surface a "writes will be lost" warning to the
      // 95%+ of browsers that support OPFS or IndexedDB. The few that
      // still fall back to in-memory will lose state on tab close —
      // acceptable for an alpha-tier surface.
      return const PlatformCaps(
        hasLocalPty: false,
        hasFileSystem: false,
        hasNotifications: false,
        supportsBackgroundIsolates: true,
        hasPersistentSqlite: true,
        hasEmbeddedWebView: false,
        hasRepoAppRunner: false,
        hasSystemShare: true, // navigator.share
      );
    }
    final desktop = switch (defaultTargetPlatform) {
      TargetPlatform.macOS => true,
      TargetPlatform.linux => true,
      TargetPlatform.windows => true,
      _ => false,
    };
    return PlatformCaps(
      hasLocalPty: desktop, // mobile sandboxes don't allow process spawn
      hasFileSystem: desktop,
      hasNotifications: true,
      supportsBackgroundIsolates: true,
      hasPersistentSqlite: true,
      hasEmbeddedWebView: switch (defaultTargetPlatform) {
        TargetPlatform.macOS ||
        TargetPlatform.iOS ||
        TargetPlatform.android =>
          true,
        _ => false,
      },
      hasRepoAppRunner: switch (defaultTargetPlatform) {
        TargetPlatform.macOS || TargetPlatform.linux => true,
        _ => false,
      },
      isMobile: switch (defaultTargetPlatform) {
        TargetPlatform.iOS || TargetPlatform.android => true,
        _ => false,
      },
      hasRichClipboard: defaultTargetPlatform == TargetPlatform.macOS,
      editorPlatform: switch (defaultTargetPlatform) {
        TargetPlatform.iOS => 'ios',
        TargetPlatform.android => 'android',
        TargetPlatform.macOS => 'macos',
        _ => 'web',
      },
      hasSystemShare: switch (defaultTargetPlatform) {
        TargetPlatform.iOS ||
        TargetPlatform.android ||
        TargetPlatform.macOS =>
          true,
        _ => false,
      },
    );
  }

  @override
  String toString() =>
      'PlatformCaps(pty=$hasLocalPty, fs=$hasFileSystem, '
      'notif=$hasNotifications, isolate=$supportsBackgroundIsolates, '
      'sqlite=$hasPersistentSqlite, webview=$hasEmbeddedWebView, '
      'repoRunner=$hasRepoAppRunner, mobile=$isMobile, '
      'richClipboard=$hasRichClipboard)';
}

/// Riverpod provider — detected once at startup. Override in tests with
/// `platformCapsProvider.overrideWithValue(PlatformCaps(...))`.
final platformCapsProvider = Provider<PlatformCaps>((ref) {
  return PlatformCaps.detect();
});
