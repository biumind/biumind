// Reviews controller — wiki audit queue state for the UI.
//
// Backend: brain.review_items table populated by the dedup / lint /
// sweep workers + manual page_merger calls. Client owns no local
// cache for this — the queue changes asynchronously via background
// workers, and a stale view here would mislead the user. We pull on
// page open + on every action; intervening list refreshes reuse the
// existing data while the next pull lands.

import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:meta/meta.dart';

import '../../../data/api/reviews_client.dart';
import '../../../data/reviews_providers.dart';
import '../../../data/wiki_providers.dart';
import '../../../data/wiki_repository.dart';

/// Filter values the page exposes via tabs. "all" is mostly for
/// debugging — the UI defaults to a kind-specific tab.
enum ReviewsKindFilter { all, dedup, lint, sweep }

extension ReviewsKindFilterX on ReviewsKindFilter {
  String? toQuery() => switch (this) {
        ReviewsKindFilter.all => null,
        ReviewsKindFilter.dedup => 'dedup',
        ReviewsKindFilter.lint => 'lint',
        ReviewsKindFilter.sweep => 'sweep',
      };

  String label() => switch (this) {
        ReviewsKindFilter.all => '全部',
        ReviewsKindFilter.dedup => '重复 (dedup)',
        ReviewsKindFilter.lint => '质量 (lint)',
        ReviewsKindFilter.sweep => '陈旧 (sweep)',
      };
}

@immutable
class ReviewsState {
  final List<WikiReview> reviews;
  final ReviewsKindFilter kind;
  final bool noCredentials;
  final String? lastError;
  // Set of ids currently being acted on (resolve/dismiss/merge). The UI
  // greys out the corresponding row's buttons so a double-click can't
  // fire two server calls.
  final Set<String> pending;

  const ReviewsState({
    this.reviews = const [],
    this.kind = ReviewsKindFilter.dedup,
    this.noCredentials = false,
    this.lastError,
    this.pending = const {},
  });

  ReviewsState copyWith({
    List<WikiReview>? reviews,
    ReviewsKindFilter? kind,
    bool? noCredentials,
    Object? lastError = _unset,
    Set<String>? pending,
  }) {
    return ReviewsState(
      reviews: reviews ?? this.reviews,
      kind: kind ?? this.kind,
      noCredentials: noCredentials ?? this.noCredentials,
      lastError: lastError == _unset
          ? this.lastError
          : (lastError as String?),
      pending: pending ?? this.pending,
    );
  }
}

const _unset = Object();

class ReviewsController extends AutoDisposeAsyncNotifier<ReviewsState> {
  @override
  Future<ReviewsState> build() async {
    // select(endpoint): token 轮换不重拉 (cleanup 页不每小时闪)。
    ref.watch(reviewsClientProvider.select((c) => c?.baseUrl));
    final client = ref.read(reviewsClientProvider);
    if (client == null) {
      return const ReviewsState(noCredentials: true);
    }
    final activeProject = await _activeProject();
    if (activeProject == null) {
      return const ReviewsState();
    }
    final reviews = await client.list(
      projectId: activeProject.id,
      kind: ReviewsKindFilter.dedup.toQuery(),
    );
    return ReviewsState(reviews: reviews);
  }

  Future<RepoProject?> _activeProject() async {
    // Mirror wiki_controller: pull the first project as default. The
    // wiki page already has its own picker; reviews follows whatever
    // wiki currently treats as "active". When the wiki UI lands a
    // proper "active project" provider this hop becomes a watch.
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null) return null;
    final projects = await repo.watchProjects().first;
    if (projects.isEmpty) return null;
    return projects.first;
  }

  /// Switch the kind filter and refetch. Stays optimistic about the
  /// new tab — UI shows a spinner via the AsyncValue.loading state.
  Future<void> setKind(ReviewsKindFilter kind) async {
    state = const AsyncValue.loading();
    state = await AsyncValue.guard(() async {
      final client = ref.read(reviewsClientProvider);
      if (client == null) return const ReviewsState(noCredentials: true);
      final project = await _activeProject();
      if (project == null) return ReviewsState(kind: kind);
      final reviews = await client.list(
        projectId: project.id,
        kind: kind.toQuery(),
      );
      return ReviewsState(kind: kind, reviews: reviews);
    });
  }

  /// Re-pull current view. Cheap; users hit the refresh button to
  /// pick up changes from the dedup/lint/sweep workers running in
  /// the background.
  Future<void> refresh() async {
    final cur = state.valueOrNull;
    final kind = cur?.kind ?? ReviewsKindFilter.dedup;
    await setKind(kind);
  }

  Future<void> dismiss(String reviewId) async {
    await _act(reviewId, (c) => c.dismiss(reviewId));
  }

  Future<void> resolve(String reviewId) async {
    await _act(reviewId, (c) => c.resolve(reviewId));
  }

  /// Merge two pages and auto-resolve the related dedup review.
  Future<void> merge({
    required String reviewId,
    required String canonicalId,
    required String duplicateId,
  }) async {
    await _act(reviewId, (c) async {
      await c.mergePages(
        canonicalId: canonicalId,
        duplicateId: duplicateId,
      );
    });
  }

  /// Turn a contradiction review into a `kind:query` page for human
  /// adjudication (frontmatter origin=contradiction-review + review_id),
  /// body = "# {title}\n\n{description}", then resolve the review.
  /// Mirrors reference review-create-page.ts:64. The agent never auto-
  /// fixes contradictions — they land here for a human to call.
  Future<void> createQueryPage(WikiReview review) async {
    await _act(review.id, (c) async {
      final repo = ref.read(wikiRepositoryProvider);
      final wiki = repo?.client;
      if (wiki == null) return;
      final title = review.title.isEmpty ? '查询：矛盾' : review.title;
      final page = await wiki.createPage(
        review.projectId,
        title: title,
        frontmatter: <String, dynamic>{
          'kind': 'query',
          'origin': 'contradiction-review',
          'review_id': review.id,
        },
      );
      await wiki.updatePageBody(
        review.projectId,
        page.id,
        ifMatchVersion: page.version,
        bodyMd: '# $title\n\n${review.description}',
      );
      await c.resolve(review.id);
    });
  }

  /// Trigger an on-demand lint scan (POST /reviews/scan). `structural`
  /// runs synchronously and returns the count of newly-created findings;
  /// `semantic` queues a background LLM pass. Returns the scan result so
  /// the UI can surface "新增 N 条" / "后台处理中", then refreshes the
  /// list. Replaces the deleted /lint/run + /lint/semantic pair (B-10).
  Future<LintScanResult?> scanLint(String family) async {
    final project = await _activeProject();
    if (project == null) return null;
    final client = ref.read(reviewsClientProvider);
    if (client == null) return null;
    try {
      final res = await client.triggerScan(
        projectId: project.id,
        family: family,
      );
      await refresh();
      return res;
    } catch (e) {
      final cur = state.valueOrNull;
      if (cur != null) {
        state = AsyncValue.data(cur.copyWith(lastError: e.toString()));
      }
      return null;
    }
  }

  /// Optimistic pending set + best-effort refresh. We don't surgically
  /// remove the row from local state on success — the next list call
  /// is the source of truth for "what's still open".
  Future<void> _act(
    String reviewId,
    Future<void> Function(ReviewsClient) work,
  ) async {
    final cur = state.valueOrNull;
    if (cur == null) return;
    final client = ref.read(reviewsClientProvider);
    if (client == null) return;
    final pending = {...cur.pending, reviewId};
    state = AsyncValue.data(cur.copyWith(pending: pending, lastError: null));
    try {
      await work(client);
      await refresh();
    } catch (e) {
      // Restore the original state minus the pending flag so the row
      // re-enables. Surface the error string for the snackbar.
      final still = state.valueOrNull ?? cur;
      final stillPending = {...still.pending}..remove(reviewId);
      state = AsyncValue.data(still.copyWith(
        pending: stillPending,
        lastError: e.toString(),
      ));
    }
  }
}

final reviewsControllerProvider =
    AsyncNotifierProvider.autoDispose<ReviewsController, ReviewsState>(
  ReviewsController.new,
);
