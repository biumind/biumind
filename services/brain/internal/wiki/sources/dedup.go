// source 级判重：同项目同 content_hash 的兄弟源 → kind=dedup 的 review_item。
//
// 供两处调用（保持行为一致）：
//   - ingest/internal_api.go：wiki-parse worker parse done 回写后
//   - sources/api.go：客户端 docproc 本机解析随 source 提交 extracted_text +
//     content_hash 时（00007 client-docproc）
package sources

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"

	"github.com/biumind/biumind/services/brain/internal/wiki/reviews"
	"github.com/google/uuid"
)

// DetectSourceDupes 查同项目同 content_hash 的兄弟行，命中则写一条
// kind=dedup 的 review_item。dedupe_key 维度 = 项目内（pid + hash），
// 故跨项目不判重（与 files.objects 按 user dedup 的隐私隔离一致）。
// page_ids 留空（source dedup 不绑 page）；source_ids 进 payload 供前端展开。
//
// 失败只 warn —— parse/建源已成功，dedup 漏检不阻塞主路径。
func DetectSourceDupes(
	ctx context.Context,
	s *Store,
	rev *reviews.Store,
	logger *slog.Logger,
	src *Source,
	contentHash []byte,
) {
	if src.UserID == nil || rev == nil {
		return
	}
	dupes, err := s.FindSourceDupes(ctx, src.ProjectID, contentHash, src.ID)
	if err != nil {
		logger.Warn("source dedup query failed", "source_id", src.ID, "err", err)
		return
	}
	if len(dupes) == 0 {
		return
	}
	hashHex := hex.EncodeToString(contentHash)
	dedupeKey := fmt.Sprintf("dedup:source:%s:%s", src.ProjectID.String(), hashHex)
	otherNames := make([]string, 0, len(dupes))
	sourceIDs := []uuid.UUID{src.ID}
	idStrs := []string{src.ID.String()}
	for _, d := range dupes {
		otherNames = append(otherNames, d.Filename)
		sourceIDs = append(sourceIDs, d.ID)
		idStrs = append(idStrs, d.ID.String())
	}
	title := fmt.Sprintf("源文件重复：%s ↔ %s", src.Filename, otherNames[0])
	desc := fmt.Sprintf(
		"项目内 %d 个源提取文本完全相同（content_hash=%s…），建议确认是否为重复上传。",
		len(dupes)+1, hashHex[:12])
	if _, _, uerr := rev.Upsert(ctx, reviews.UpsertInput{
		ProjectID:   src.ProjectID,
		OwnerID:     *src.UserID,
		Kind:        reviews.KindDedup,
		Title:       title,
		Description: desc,
		PageIDs:     []uuid.UUID{},
		Payload: map[string]any{
			"kind":         "source",
			"content_hash": hashHex,
			"source_ids":   idStrs,
			"source_uuids": sourceIDs,
		},
		DedupeKey: dedupeKey,
	}); uerr != nil {
		logger.Warn("source dedup review write failed",
			"source_id", src.ID, "err", uerr)
	}
}
