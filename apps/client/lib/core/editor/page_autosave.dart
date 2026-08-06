/// Debounced auto-save controller for the page editor.
///
/// Extracted as its own pure type so the debounce semantics are
/// testable without spinning up a Flutter widget tree. The controller
/// owns one [Timer]; ``schedule()`` resets it; ``flush()`` cancels
/// the pending timer and runs the saver synchronously; ``cancel()``
/// drops the pending save.
///
/// Lifecycle:
///   * caller wires editor onChanged → ``schedule(content)``
///   * after [debounce] of inactivity, the saver runs
///   * caller invokes ``flush()`` on widget dispose so half-typed
///     edits aren't lost when the user navigates away
///
/// Status changes are exposed via [statusListener] so the UI can
/// render "saving / saved / error" indicators without re-rendering
/// the editor.
library;

import 'dart:async';

import 'package:flutter/foundation.dart';

enum AutoSaveStatus { idle, saving, saved, error, conflict }

typedef AutoSaveFn = Future<AutoSaveOutcome> Function(String content);

@immutable
class AutoSaveOutcome {
  const AutoSaveOutcome({
    required this.status,
    this.errorMessage,
  });
  final AutoSaveStatus status;
  final String? errorMessage;
}

class AutoSaveController {
  AutoSaveController({
    required this.saver,
    this.debounce = const Duration(milliseconds: 1500),
  });

  final AutoSaveFn saver;
  final Duration debounce;

  Timer? _timer;
  String? _pendingContent;
  bool _disposed = false;

  AutoSaveStatus _status = AutoSaveStatus.idle;
  String? _errorMessage;
  final List<VoidCallback> _statusListeners = <VoidCallback>[];

  AutoSaveStatus get status => _status;
  String? get errorMessage => _errorMessage;

  void addListener(VoidCallback cb) {
    _statusListeners.add(cb);
  }

  void removeListener(VoidCallback cb) {
    _statusListeners.remove(cb);
  }

  /// Schedule a save in [debounce] from now. Subsequent calls reset
  /// the timer so a steadily-typing user only saves once they pause.
  void schedule(String content) {
    if (_disposed) return;
    _pendingContent = content;
    _setStatus(AutoSaveStatus.idle);
    _timer?.cancel();
    _timer = Timer(debounce, _fire);
  }

  /// Run any pending save synchronously. Useful before navigation
  /// or widget disposal.
  Future<void> flush() async {
    if (_timer == null && _pendingContent == null) return;
    _timer?.cancel();
    _timer = null;
    await _fire();
  }

  /// Drop any pending save without running it.
  void cancel() {
    _timer?.cancel();
    _timer = null;
    _pendingContent = null;
  }

  Future<void> _fire() async {
    if (_disposed) return;
    final content = _pendingContent;
    if (content == null) return;
    _pendingContent = null;
    _setStatus(AutoSaveStatus.saving);
    try {
      final outcome = await saver(content);
      if (_disposed) return;
      _errorMessage = outcome.errorMessage;
      _setStatus(outcome.status);
    } catch (e) {
      // catch (e) 捕获 Object：StateError 等 Error 子类（如 repository
      // 的 'note not found'）也要落到 error 态 —— 只捕 Exception 会让
      // 状态永远卡在 saving，内容静默丢失。
      if (_disposed) return;
      _errorMessage = e.toString();
      _setStatus(AutoSaveStatus.error);
    }
  }

  void _setStatus(AutoSaveStatus next) {
    if (_status == next) return;
    _status = next;
    for (final cb in List<VoidCallback>.from(_statusListeners)) {
      cb();
    }
  }

  void dispose() {
    _disposed = true;
    _timer?.cancel();
    _statusListeners.clear();
  }
}
