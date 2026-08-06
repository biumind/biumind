// SelectionEditClient — S3 P1-6 inline selection edit/ask on a wiki page.
//
//   POST /v1/wiki/projects/{pid}/pages/{id}/selection-edit
//
// Phase A ships `edit` only (rewrite the selected span). `ask` (KB top5 +
// [1][2] citations) lands in phase B — see selection_edit.go handleSelectionEdit.

import '_http_helpers.dart';

class SelectionEditClient {
  SelectionEditClient(this.baseUrl, this.bearerToken);

  final Uri baseUrl;
  final String bearerToken;

  /// Rewrite the selected span per `instruction`. Returns the replacement
  /// text (outer markdown fence already stripped server-side). Host writes
  /// it back via EditorBridgeController.replaceSelection.
  Future<String> edit(
    String projectId,
    String pageId, {
    required String selection,
    required String instruction,
    String before = '',
    String after = '',
  }) async {
    final m = await apiRequest(
      method: 'POST',
      url: baseUrl.replace(
        path: '/v1/wiki/projects/$projectId/pages/$pageId/selection-edit',
      ),
      bearerToken: bearerToken,
      body: {
        'mode': 'edit',
        'selection': selection,
        'before': before,
        'after': after,
        'instruction': instruction,
      },
    );
    return (m['replacement'] as String?) ?? '';
  }

  /// Ask the model about the selected span, grounded in same-project KB
  /// (top5 BM25 hits). Returns the answer markdown + citations ([N] → page).
  Future<({String answer, List<SelectionCitation> citations})> ask(
    String projectId,
    String pageId, {
    required String selection,
    required String instruction,
    String before = '',
    String after = '',
  }) async {
    final m = await apiRequest(
      method: 'POST',
      url: baseUrl.replace(
        path: '/v1/wiki/projects/$projectId/pages/$pageId/selection-edit',
      ),
      bearerToken: bearerToken,
      body: {
        'mode': 'ask',
        'selection': selection,
        'before': before,
        'after': after,
        'instruction': instruction,
      },
    );
    final answer = (m['answer'] as String?) ?? '';
    final citations = ((m['citations'] as List?) ?? const [])
        .cast<Map<String, dynamic>>()
        .map(SelectionCitation.fromJson)
        .toList();
    return (answer: answer, citations: citations);
  }
}

class SelectionCitation {
  const SelectionCitation({
    required this.n,
    required this.pageId,
    required this.title,
  });
  final int n;
  final String pageId;
  final String title;

  factory SelectionCitation.fromJson(Map<String, dynamic> j) =>
      SelectionCitation(
        n: (j['n'] as num?)?.toInt() ?? 0,
        pageId: (j['page_id'] as String?) ?? '',
        title: (j['title'] as String?) ?? '',
      );
}
