// Parsers turn raw Source.Content into a ParsedDoc.
//
// MVP: markdown / html / plain. PDF / DOCX / etc are deferred to a separate
// (likely Python) worker since Go's PDF ecosystem is weaker.
package ingest

import (
	"errors"
	"regexp"
	"strings"
)

// Parse dispatches to the right parser by Source.Kind.
func Parse(s Source) (*ParsedDoc, error) {
	switch s.Kind {
	case KindMarkdown:
		return parseMarkdown(s)
	case KindHTML:
		return parseHTML(s)
	case KindPlainText, "":
		return parsePlain(s)
	}
	return nil, errors.New("ingest: unsupported source kind: " + string(s.Kind))
}

// ─── Markdown ───────────────────────────────────────────

var mdHeading = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*$`)

func parseMarkdown(s Source) (*ParsedDoc, error) {
	doc := &ParsedDoc{Title: s.Title}
	var (
		curHeading string
		curBuf     strings.Builder
		pos        float64 = 1
	)
	flush := func() {
		text := strings.TrimSpace(curBuf.String())
		if text == "" {
			return
		}
		doc.Chunks = append(doc.Chunks, Chunk{
			Heading: curHeading, Text: text, Position: pos,
		})
		pos++
		curBuf.Reset()
	}
	for _, raw := range strings.Split(s.Content, "\n") {
		line := strings.TrimRight(raw, "\r")
		if m := mdHeading.FindStringSubmatch(line); m != nil {
			flush()
			level := len(m[1])
			heading := strings.TrimSpace(m[2])
			if doc.Title == "" && level == 1 {
				doc.Title = heading
				curHeading = ""
			} else {
				curHeading = heading
			}
			continue
		}
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if curBuf.Len() > 0 {
			curBuf.WriteByte('\n')
		}
		curBuf.WriteString(line)
	}
	flush()
	if doc.Title == "" {
		doc.Title = firstNonEmptyLine(s.Content, "Untitled")
	}
	return doc, nil
}

// ─── HTML ───────────────────────────────────────────────

var (
	tagRE        = regexp.MustCompile(`(?is)<[^>]+>`)
	titleRE      = regexp.MustCompile(`(?is)<title[^>]*>(.+?)</title>`)
	scriptRE     = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	styleRE      = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	whitespaceRE = regexp.MustCompile(`[ \t]+`)
)

func parseHTML(s Source) (*ParsedDoc, error) {
	doc := &ParsedDoc{Title: s.Title}
	body := scriptRE.ReplaceAllString(s.Content, "")
	body = styleRE.ReplaceAllString(body, "")
	if doc.Title == "" {
		if m := titleRE.FindStringSubmatch(body); len(m) > 1 {
			doc.Title = strings.TrimSpace(m[1])
		}
	}
	plain := tagRE.ReplaceAllString(body, "\n")
	plain = strings.ReplaceAll(plain, " ", " ")
	plain = whitespaceRE.ReplaceAllString(plain, " ")

	var pos float64 = 1
	for _, p := range strings.Split(plain, "\n") {
		t := strings.TrimSpace(p)
		if t == "" {
			continue
		}
		doc.Chunks = append(doc.Chunks, Chunk{Text: t, Position: pos})
		pos++
	}
	if doc.Title == "" {
		doc.Title = firstNonEmptyLine(s.Content, "Untitled")
	}
	return doc, nil
}

// ─── Plain ──────────────────────────────────────────────

func parsePlain(s Source) (*ParsedDoc, error) {
	doc := &ParsedDoc{Title: s.Title}
	if doc.Title == "" {
		doc.Title = firstNonEmptyLine(s.Content, "Untitled")
	}
	var pos float64 = 1
	for _, p := range strings.Split(s.Content, "\n\n") {
		t := strings.TrimSpace(p)
		if t == "" {
			continue
		}
		doc.Chunks = append(doc.Chunks, Chunk{Text: t, Position: pos})
		pos++
	}
	return doc, nil
}

func firstNonEmptyLine(s, fallback string) string {
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if t != "" {
			if len(t) > 80 {
				return t[:80] + "…"
			}
			return t
		}
	}
	return fallback
}
