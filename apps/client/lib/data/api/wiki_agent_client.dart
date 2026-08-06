// WikiAgentClient — drives brain's wiki autonomous-maintenance agent loop
// (S3 P0-1). One origin, same brain endpoint family as ResearchClient.
//
//   POST /v1/wiki/projects/{pid}/agent/run          SSE stream (read → mutate → backlinks)
//   POST /v1/wiki/projects/{pid}/agent/run/cancel   stop one run by run_id
//
// Reuses _http_helpers.sseStream (401 refresh + reopen) and
// ChatClient.parseSse (BlockEmitter v2 event decode) so the wiki agent
// dialog renders the SAME ChatStreamEvent shape chat already handles — no
// parallel event taxonomy, no second SSE parser.

import '_http_helpers.dart';
import 'chat_client.dart';

class WikiAgentClient {
  WikiAgentClient(this.baseUrl, this.bearerToken);

  final Uri baseUrl;
  final String bearerToken;

  /// Stream the maintenance agent loop. Yields the same [ChatStreamEvent]
  /// variants chat consumes: block.create / block.delta / block.complete /
  /// tool.created / tool.completed / message.done / block.error /
  /// ChatStreamError. Caller cancels by stopping iteration AND calling
  /// [cancelRun] — closing the stream alone does NOT stop the server loop
  /// (the run's hubCtx is detached from the request ctx by design).
  Stream<ChatStreamEvent> runAgentStream(
    String projectId, {
    required String runId,
    required String instruction,
    required String model,
    String mode = 'standard',
  }) {
    final url =
        baseUrl.replace(path: '/v1/wiki/projects/$projectId/agent/run');
    final body = <String, dynamic>{
      'run_id': runId,
      'instruction': instruction,
      'model': model,
      'mode': mode,
    };
    return ChatClient.parseSse(
      sseStream(url: url, bearerToken: bearerToken, body: body),
    );
  }

  /// Best-effort cancel. The loop stops at its next cancel point (an
  /// in-flight model-relay turn finishes first). Returns normally on 200;
  /// throws [ApiError] on 404 `run_not_found` (run already finished /
  /// cancelled / unknown) — callers should treat 404 as "nothing to stop"
  /// and swallow it.
  Future<void> cancelRun(String projectId, String runId) async {
    await apiRequest(
      method: 'POST',
      url: baseUrl.replace(
        path: '/v1/wiki/projects/$projectId/agent/run/cancel',
      ),
      bearerToken: bearerToken,
      body: {'run_id': runId},
    );
  }
}
