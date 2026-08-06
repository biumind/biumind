// Row scanner — split out so picker.go stays focused on algorithm.

package today

import (
	"encoding/json"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"
)

// embeddingVec is unexported and lives on Entry; see types here so
// picker.go's scanEntry can populate it without exposing the pgvector
// type to JSON consumers.
type embeddingHolder = *pgvector.Vector

// embeddingVec is filled by scanEntry. The struct field exists on
// Entry (declared in picker.go) but kept lower-case so JSON encoders
// and clients never see it. We add it via this file's package-private
// extension helper to keep the picker.go declaration uncluttered.
func setEmbedding(e *Entry, v embeddingHolder) {
	e.embeddingVec = v
}

func scanEntry(rows pgx.Rows) (*Entry, error) {
	var e Entry
	var bulletsJSON []byte
	var emb pgvector.Vector
	if err := rows.Scan(
		&e.ID, &e.FeedID, &e.FeedTitle, &e.FeedColor,
		&e.URL, &e.Title, &e.Author,
		&e.Snippet,
		&e.AITakeaway, &bulletsJSON, &e.AITopics,
		&e.AIImportance, &e.WordCount, &e.ReadingSec,
		&e.PublishedAt, &e.FetchedAt,
		&emb,
	); err != nil {
		return nil, err
	}
	if len(bulletsJSON) > 0 {
		_ = json.Unmarshal(bulletsJSON, &e.AIBullets)
	}
	if v := emb.Slice(); len(v) > 0 {
		setEmbedding(&e, &emb)
	}
	// Snippet was joined as content_text|content_html; trim HTML
	// for cleaner display + cap length.
	if e.Snippet != "" {
		e.Snippet = trimSnippet(e.Snippet, 280)
	}
	return &e, nil
}

func trimSnippet(s string, max int) string {
	in := false
	out := strings.Builder{}
	for _, r := range s {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
		case !in:
			out.WriteRune(r)
			if out.Len() >= max {
				out.WriteString("…")
				return out.String()
			}
		}
	}
	return out.String()
}
