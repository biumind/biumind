// Tests for the presign + PUT + finalize flow on FilesClient.
//
// Spins up two in-process HttpServers (brain + MinIO stand-in) so we
// can verify:
//   * presign-upload request shape (auth header, body fields)
//   * PUT to MinIO carries no Authorization (presigned URL self-signs)
//   * PUT carries the headers from server response (Content-Type lock)
//   * finalize sends sha256 + size, returns parsed UploadResult
//   * uploadViaPresign orchestrates all three steps
//   * dedup response surfaces correctly
//   * failures clean up via DELETE /v1/files/{id}

import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/code/data/files_client.dart';

/// Minimal fake brain + minio over loopback. Records every request so
/// each test can assert on the exact wire shape.
class _FakeBackend {
  late HttpServer brain;
  late HttpServer minio;
  Uri get brainBase => Uri.parse('http://${brain.address.host}:${brain.port}');
  Uri get minioBase => Uri.parse('http://${minio.address.host}:${minio.port}');

  final calls = <_Call>[];

  /// File id the brain mock returns from presign-upload (override in tests).
  String pendingFileId = 'file-001';

  /// Used to fail PUT (simulate MinIO down).
  int? minioPutStatus;

  /// finalize response — set per-test.
  Map<String, dynamic> finalizeResponse = {
    'file_id': 'file-001',
    'sha256': '',
    'size_bytes': 0,
    'mime_type': 'image/png',
    'deduped': false,
  };
  int finalizeStatus = 200;

  /// presign-upload response error override.
  int? presignStatusOverride;
  String presignBodyOverride = '';

  Future<void> start() async {
    brain = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    minio = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    _serveBrain();
    _serveMinio();
  }

  Future<void> stop() async {
    await brain.close(force: true);
    await minio.close(force: true);
  }

  void _serveBrain() {
    brain.listen((req) async {
      final body = await utf8.decoder.bind(req).join();
      calls.add(_Call(
        host: 'brain',
        method: req.method,
        path: req.uri.path,
        headers: _captureHeaders(req.headers),
        body: body,
      ));

      if (req.method == 'POST' && req.uri.path == '/v1/files/presign-upload') {
        if (presignStatusOverride != null) {
          req.response.statusCode = presignStatusOverride!;
          req.response.write(presignBodyOverride);
          await req.response.close();
          return;
        }
        final claimed = jsonDecode(body) as Map<String, dynamic>;
        final mime = claimed['mime'] as String;
        // signed URL points back at our minio mock with a fake signature
        final signedURL = minioBase.replace(
          path: '/biumind-files/$pendingFileId',
          queryParameters: {'X-Amz-Signature': 'fake'},
        );
        req.response.headers.contentType = ContentType.json;
        req.response.write(jsonEncode({
          'file_id': pendingFileId,
          'upload_url': signedURL.toString(),
          'headers': {'Content-Type': mime},
          'object_key': 'u/$pendingFileId',
          'expires_at': DateTime.now()
              .toUtc()
              .add(const Duration(minutes: 15))
              .toIso8601String(),
        }));
        await req.response.close();
        return;
      }
      if (req.method == 'POST' && req.uri.path == '/v1/files/finalize') {
        req.response.statusCode = finalizeStatus;
        if (finalizeStatus == 200) {
          req.response.headers.contentType = ContentType.json;
          req.response.write(jsonEncode(finalizeResponse));
        }
        await req.response.close();
        return;
      }
      if (req.method == 'DELETE' && req.uri.path.startsWith('/v1/files/')) {
        req.response.statusCode = 204;
        await req.response.close();
        return;
      }
      req.response.statusCode = 404;
      await req.response.close();
    });
  }

  void _serveMinio() {
    minio.listen((req) async {
      final body = await req.fold<List<int>>(<int>[], (acc, c) => acc..addAll(c));
      calls.add(_Call(
        host: 'minio',
        method: req.method,
        path: req.uri.path,
        headers: _captureHeaders(req.headers),
        body: utf8.decode(body, allowMalformed: true),
        bodyBytes: body,
      ));
      req.response.statusCode = minioPutStatus ?? 200;
      await req.response.close();
    });
  }
}

Map<String, String> _captureHeaders(HttpHeaders h) {
  final out = <String, String>{};
  h.forEach((name, values) {
    out[name.toLowerCase()] = values.join(',');
  });
  return out;
}

class _Call {
  _Call({
    required this.host,
    required this.method,
    required this.path,
    required this.headers,
    required this.body,
    this.bodyBytes,
  });
  final String host;
  final String method;
  final String path;
  final Map<String, String> headers;
  final String body;
  final List<int>? bodyBytes;
}

void main() {
  late _FakeBackend be;

  setUp(() async {
    be = _FakeBackend();
    await be.start();
  });

  tearDown(() async {
    await be.stop();
  });

  test('uploadViaPresign happy path: 3 calls in order, no auth on PUT', () async {
    final payload = utf8.encode('fake-png-bytes');
    final sha = sha256.convert(payload).toString();
    be.pendingFileId = 'file-happy';
    be.finalizeResponse = {
      'file_id': 'file-happy',
      'sha256': sha,
      'size_bytes': payload.length,
      'mime_type': 'image/png',
      'deduped': false,
    };

    final client = FilesClient(be.brainBase, 'token-xyz');
    final result = await client.uploadViaPresign(
      bytes: payload,
      filename: 'shot.png',
      mime: 'image/png',
    );

    expect(result.fileId, 'file-happy');
    expect(result.deduped, isFalse);
    expect(result.sha256, sha);

    // Three calls: presign → MinIO PUT → finalize
    expect(be.calls.length, 3, reason: 'unexpected call sequence: ${be.calls.map((c) => '${c.method} ${c.host}${c.path}').toList()}');
    expect(be.calls[0].host, 'brain');
    expect(be.calls[0].path, '/v1/files/presign-upload');
    expect(be.calls[0].headers['authorization'], 'Bearer token-xyz');

    expect(be.calls[1].host, 'minio');
    expect(be.calls[1].method, 'PUT');
    expect(be.calls[1].headers['authorization'], isNull,
        reason: 'PUT to MinIO must NOT carry Authorization (presigned URL self-signs)');
    expect(be.calls[1].headers['content-type'], 'image/png');
    expect(be.calls[1].bodyBytes, payload);

    expect(be.calls[2].host, 'brain');
    expect(be.calls[2].path, '/v1/files/finalize');
    final finalizeBody = jsonDecode(be.calls[2].body) as Map<String, dynamic>;
    expect(finalizeBody['file_id'], 'file-happy');
    expect(finalizeBody['sha256'], sha);
    expect(finalizeBody['size'], payload.length);
  });

  test('uploadViaPresign surfaces deduped flag', () async {
    be.pendingFileId = 'file-dup-pending';
    be.finalizeResponse = {
      'file_id': 'existing-aaa',
      'sha256': sha256.convert(utf8.encode('dup')).toString(),
      'size_bytes': 3,
      'deduped': true,
    };
    final client = FilesClient(be.brainBase, 'tk');
    final result = await client.uploadViaPresign(
      bytes: utf8.encode('dup'),
      filename: 'a.png',
      mime: 'image/png',
    );
    expect(result.deduped, isTrue);
    expect(result.fileId, 'existing-aaa',
        reason: 'dedup must return the existing object id, not the pending one');
  });

  test('presign 4xx surfaces FilesApiError without touching MinIO', () async {
    be.presignStatusOverride = 400;
    be.presignBodyOverride =
        '{"error":{"code":"mime_not_allowed","message":"x"}}';
    final client = FilesClient(be.brainBase, 'tk');
    await expectLater(
      () => client.uploadViaPresign(
        bytes: utf8.encode('x'),
        filename: 'a.exe',
        mime: 'application/x-msdownload',
      ),
      throwsA(isA<FilesApiError>().having((e) => e.status, 'status', 400)),
    );
    // Only one call — MinIO never reached.
    expect(be.calls.where((c) => c.host == 'minio').toList(), isEmpty);
  });

  test('MinIO PUT failure triggers DELETE cleanup of pending row', () async {
    be.pendingFileId = 'file-cleanup';
    be.minioPutStatus = 500;
    final client = FilesClient(be.brainBase, 'tk');

    await expectLater(
      () => client.uploadViaPresign(
        bytes: utf8.encode('payload'),
        filename: 'a.png',
        mime: 'image/png',
      ),
      throwsA(isA<FilesApiError>()),
    );

    // Wait briefly for the unawaited DELETE to land — the orchestrator
    // schedules it but doesn't await; tearDown stops the server otherwise.
    await Future<void>.delayed(const Duration(milliseconds: 50));

    final deleteCall = be.calls.firstWhere(
      (c) => c.method == 'DELETE' && c.path.endsWith('/file-cleanup'),
      orElse: () => _Call(
          host: 'none', method: '', path: '', headers: {}, body: ''),
    );
    expect(deleteCall.host, 'brain',
        reason: 'PUT failure should fire DELETE /v1/files/{id} for cleanup');
  });

  test('finalize 4xx triggers cleanup', () async {
    be.pendingFileId = 'file-fin-bad';
    be.finalizeStatus = 400;
    final client = FilesClient(be.brainBase, 'tk');
    await expectLater(
      () => client.uploadViaPresign(
        bytes: utf8.encode('payload'),
        filename: 'a.png',
        mime: 'image/png',
      ),
      throwsA(isA<FilesApiError>()),
    );
    await Future<void>.delayed(const Duration(milliseconds: 50));
    final hasDelete = be.calls.any(
        (c) => c.method == 'DELETE' && c.path.endsWith('/file-fin-bad'));
    expect(hasDelete, isTrue);
  });

  test('presignUpload sends source + metadata correctly', () async {
    be.pendingFileId = 'file-meta';
    be.finalizeResponse = {
      'file_id': 'file-meta',
      'sha256': sha256.convert(utf8.encode('p')).toString(),
      'size_bytes': 1,
      'deduped': false,
    };
    final client = FilesClient(be.brainBase, 'tk');
    await client.uploadViaPresign(
      bytes: utf8.encode('p'),
      filename: 'x.png',
      mime: 'image/png',
      source: 'chat-attachment',
      metadata: {'thread_id': 't-1', 'origin': 'paste'},
    );
    final presignBody =
        jsonDecode(be.calls.first.body) as Map<String, dynamic>;
    expect(presignBody['source'], 'chat-attachment');
    expect(presignBody['metadata'], {'thread_id': 't-1', 'origin': 'paste'});
    expect(presignBody['mime'], 'image/png');
    expect(presignBody['size'], 1);
  });
}
