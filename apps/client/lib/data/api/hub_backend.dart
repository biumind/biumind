// RelayBackend implements AiSurfaceBackend by streaming model-relay /v1/messages.
//
// Wire format (matches services/model-relay/internal/api/messages.go):
//
//   event: delta            data: {"text":"..."}
//   event: tool_call_start  data: {"id":"...","name":"..."}
//   event: tool_call_args   data: {"id":"...","delta":"..."}
//   event: tool_call_end    data: {"id":"..."}
//   event: stop             data: {"reason":"end_turn"}
//   event: end              data: {}
//   event: error            data: <raw>
//
// Each AiSurfaceKind has its own model preference + system prompt; users
// override via Settings. For now we hardcode the defaults from the technical
// architecture doc (§4b.4).

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:logging/logging.dart';

import '../../core/ai/ai_surface.dart';
import '_http_helpers.dart' show authErrorHandler;

class HubConfig {
  final Uri endpoint;       // e.g. https://api.biu.app  (no trailing slash needed)
  final String bearerToken; // JWT or virtual key
  final Map<AiSurfaceKind, String> modelOverrides;
  final Map<AiSurfaceKind, String> systemOverrides;

  const HubConfig({
    required this.endpoint,
    required this.bearerToken,
    this.modelOverrides = const {},
    this.systemOverrides = const {},
  });
}

class RelayBackend extends AiSurfaceBackend {
  RelayBackend(this._config) : _log = Logger('biumind.relay_backend');

  final HubConfig _config;
  final Logger _log;

  static const Map<AiSurfaceKind, String> _defaultModels = {
    AiSurfaceKind.chat: 'claude-sonnet-4-6',
    AiSurfaceKind.wikiInline: 'claude-haiku-4-5',
    AiSurfaceKind.wikiDeepResearch: 'claude-sonnet-4-6',
    AiSurfaceKind.code: 'claude-sonnet-4-6',
    AiSurfaceKind.translate: 'claude-haiku-4-5',
    AiSurfaceKind.voice: 'claude-haiku-4-5',
    AiSurfaceKind.custom: 'claude-sonnet-4-6',
  };

  static const Map<AiSurfaceKind, String> _defaultSystems = {
    AiSurfaceKind.chat:
        'You are BiuMind, a helpful AI assistant. Be concise and accurate.',
    AiSurfaceKind.wikiInline:
        'You are an inline writing assistant inside a wiki editor. Reply with plain text only — no markdown wrappers.',
    AiSurfaceKind.wikiDeepResearch:
        'You are BiuMind Research. Plan multi-step research and produce a structured report.',
    AiSurfaceKind.code:
        'You are an expert software engineer. Use available tools to inspect / modify code as needed.',
    AiSurfaceKind.translate:
        'You translate text faithfully between languages. Reply with the translation only.',
    AiSurfaceKind.voice:
        'You are a voice assistant. Reply briefly in natural spoken language.',
    AiSurfaceKind.custom: '',
  };

  String _model(AiInvocation inv) {
    return inv.policy?.preferModel ??
        _config.modelOverrides[inv.surface] ??
        _defaultModels[inv.surface] ??
        'claude-sonnet-4-6';
  }

  String _system(AiInvocation inv) {
    return _config.systemOverrides[inv.surface] ??
        _defaultSystems[inv.surface] ??
        '';
  }

  // The canonical /v1/messages body shape (matches model-relay's provider.Request).
  // We always stream.
  Map<String, dynamic> _buildBody(AiInvocation inv) {
    final messages = _toMessages(inv.input);
    return {
      'model': _model(inv),
      if (_system(inv).isNotEmpty) 'system': _system(inv),
      'messages': messages,
      'stream': true,
      'max_tokens': 4096,
    };
  }

  List<Map<String, dynamic>> _toMessages(Object input) {
    if (input is String) {
      return [
        {'role': 'user', 'content': input},
      ];
    }
    if (input is List) {
      // Already a list of {role, content} maps.
      return input.cast<Map<String, dynamic>>();
    }
    return [
      {'role': 'user', 'content': input.toString()},
    ];
  }

  /// 单次发起 SSE POST。返回 (client, resp)，调用方负责 close client。
  Future<(HttpClient, HttpClientResponse)> _openStream(Uri url, String body, String token) async {
    final client = HttpClient();
    final req = await client.postUrl(url);
    req.headers.set(HttpHeaders.contentTypeHeader, 'application/json');
    req.headers.set(HttpHeaders.acceptHeader, 'text/event-stream');
    req.headers.set(HttpHeaders.authorizationHeader, 'Bearer $token');
    req.add(utf8.encode(body));
    final resp = await req.close();
    return (client, resp);
  }

  @override
  Stream<AiChunk> invoke(AiInvocation inv) async* {
    final url = _config.endpoint.replace(path: '/v1/messages');
    final body = jsonEncode(_buildBody(inv));

    HttpClient? client;
    HttpClientResponse? resp;
    try {
      var (c, r) = await _openStream(url, body, _config.bearerToken);
      client = c;
      resp = r;

      // 401 → tokenManager.handle401 → 拿到新 token → 重开流
      if (resp.statusCode == 401 && authErrorHandler != null) {
        await resp.drain<void>();
        client.close(force: true);
        final newToken = await authErrorHandler!();
        if (newToken != null && newToken.isNotEmpty && newToken != _config.bearerToken) {
          final retry = await _openStream(url, body, newToken);
          client = retry.$1;
          resp = retry.$2;
        }
      }

      if (resp.statusCode >= 400) {
        final raw = await resp.transform(utf8.decoder).join();
        yield AiChunk.error('model-relay ${resp.statusCode}: $raw');
        return;
      }

      // Stream SSE → AiChunk
      yield* _parseSSE(resp.transform(utf8.decoder));
    } catch (e, st) {
      _log.warning('model-relay stream error', e, st);
      yield AiChunk.error(e.toString());
    } finally {
      client?.close(force: true);
    }
  }

  Stream<AiChunk> _parseSSE(Stream<String> chunks) async* {
    String currentEvent = '';
    final dataBuf = StringBuffer();
    final lineBuf = StringBuffer();

    Future<List<AiChunk>> dispatchFrame() async {
      final out = <AiChunk>[];
      if (dataBuf.isEmpty) return out;
      final data = dataBuf.toString();
      switch (currentEvent) {
        case 'delta':
          final m = jsonDecode(data) as Map<String, dynamic>;
          out.add(AiChunk.text(m['text'] as String? ?? ''));
        case 'tool_call_start':
          final m = jsonDecode(data) as Map<String, dynamic>;
          out.add(AiChunk.toolStart(
            m['id'] as String? ?? '',
            m['name'] as String? ?? '',
          ));
        case 'tool_call_args':
          final m = jsonDecode(data) as Map<String, dynamic>;
          out.add(AiChunk.toolArgs(
            m['id'] as String? ?? '',
            m['delta'] as String? ?? '',
          ));
        case 'tool_call_end':
          final m = jsonDecode(data) as Map<String, dynamic>;
          out.add(AiChunk.toolEnd(m['id'] as String? ?? ''));
        case 'end':
          out.add(AiChunk.done());
        case 'error':
          out.add(AiChunk.error(data));
        case 'stop':
          // No direct AiChunk; final state surfaces via subsequent 'end'.
          break;
      }
      return out;
    }

    await for (final chunk in chunks) {
      lineBuf.write(chunk);
      String buf = lineBuf.toString();
      int idx;
      while ((idx = buf.indexOf('\n')) >= 0) {
        final line = buf.substring(0, idx).trimRight();
        buf = buf.substring(idx + 1);
        if (line.isEmpty) {
          for (final c in await dispatchFrame()) {
            yield c;
          }
          currentEvent = '';
          dataBuf.clear();
          continue;
        }
        if (line.startsWith('event: ')) {
          currentEvent = line.substring(7);
        } else if (line.startsWith('data: ')) {
          if (dataBuf.isNotEmpty) dataBuf.write('\n');
          dataBuf.write(line.substring(6));
        }
      }
      lineBuf
        ..clear()
        ..write(buf);
    }
    // Flush trailing frame (if server didn't end with blank line)
    for (final c in await dispatchFrame()) {
      yield c;
    }
  }
}
