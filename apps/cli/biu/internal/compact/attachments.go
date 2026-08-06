// Post-compact attachment generation.
//
// After macro compact replaces the message history with a summary,
// some context is too valuable to leave only in summary form:
//
//   - Files the model was actively editing — the summary mentions
//     "we modified main.go" but doesn't include the current file
//     contents. Without re-attachment, the next Edit/Write would
//     have to re-Read first; with re-attachment, the model can
//     continue where it left off.
//   - The active plan (already wired via Auto.Options.Attachments
//     pre-MC5).
//
// Future MC iterations add: skill content, MCP instructions delta,
// agent listing delta. Each needs its own tracker subsystem; for
// MC5 the file path is the most user-impacting and the simplest
// to implement.
//
// Design choice: attachments are generated at compact time, NOT
// pre-computed. This means a file the model wrote to between Read
// and compact gets re-attached with its CURRENT on-disk content —
// the model's "memory" of the file from before its own edits is
// replaced by the post-edit truth, which is what we'd want it to
// continue working from.

package compact

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// FileTracker is the surface compact needs from AppState (or any
// equivalent store) to enumerate files the model has read this
// session. Defined as an interface so engine code stays free to
// pass either *state.AppState directly or a test stub.
type FileTracker interface {
	// TrackedFiles returns every path the model has Read this
	// session. Order is implementation-defined; this package
	// sorts the result for stable attachment ordering.
	TrackedFiles() []string
}

// MaxFileAttachmentBytes caps the per-file content size we re-inject.
// Large files would re-bloat the post-compact context immediately, so
// we truncate with a tail marker. The cap is intentionally lower than
// FileReadTool's max — those Reads happen during normal flow and
// can be expensive once; re-attaching them on every compact would
// compound.
const MaxFileAttachmentBytes = 32 * 1024 // 32 KiB per file

// MaxFileAttachmentCount caps how many files we re-attach, keeping
// the post-compact prefix tractable. Most-recently-tracked files
// win when the count exceeds this cap.
const MaxFileAttachmentCount = 10

// FileAttachment is one file's re-injection payload.
type FileAttachment struct {
	Path        string
	Content     string
	SizeBytes   int64 // size BEFORE truncation (so the model knows)
	WasTruncated bool
}

// BuildFileAttachments reads the current contents of every tracked
// file (capped at MaxFileAttachmentCount, most recent N) and
// returns a slice ready to wrap into compact attachment messages.
//
// Files that no longer exist on disk are silently skipped — the
// model knows from the summary that something happened with them,
// and re-attaching a stale "file not found" stub would be noise.
//
// Capped to last N tracked files (sort by registration order or
// alphabetically when no order info is available — TrackedFiles
// owns ordering).
func BuildFileAttachments(tracker FileTracker) []FileAttachment {
	if tracker == nil {
		return nil
	}
	paths := tracker.TrackedFiles()
	if len(paths) == 0 {
		return nil
	}
	// Stable order — alphabetical fallback. AppState's own ordering
	// is map iteration so we sort to make tests + telemetry stable.
	sort.Strings(paths)

	if len(paths) > MaxFileAttachmentCount {
		paths = paths[len(paths)-MaxFileAttachmentCount:]
	}

	var out []FileAttachment
	for _, p := range paths {
		att, ok := readForAttachment(p)
		if !ok {
			continue
		}
		out = append(out, att)
	}
	return out
}

// readForAttachment reads + truncates one file. Returns ok=false
// when the file can't be opened (deleted between Read and compact).
func readForAttachment(path string) (FileAttachment, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return FileAttachment{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return FileAttachment{}, false
	}
	att := FileAttachment{
		Path:      path,
		SizeBytes: st.Size(),
	}
	if len(data) > MaxFileAttachmentBytes {
		att.Content = string(data[:MaxFileAttachmentBytes])
		att.WasTruncated = true
	} else {
		att.Content = string(data)
	}
	return att, true
}

// FileAttachmentsAsMessages wraps a slice of FileAttachments into
// state.Messages ready to inject into the post-compact history.
// Each file becomes one system message — system role so the model
// treats the content as authoritative context, not user-supplied
// text.
//
// Empty input returns nil. Callers chain this into the
// compact.Auto.Options.Attachments closure.
func FileAttachmentsAsMessages(atts []FileAttachment) []state.Message {
	if len(atts) == 0 {
		return nil
	}
	out := make([]state.Message, 0, len(atts))
	for _, a := range atts {
		out = append(out, state.Message{
			Role: state.RoleSystem,
			Content: []state.ContentBlock{{
				Type: state.ContentText,
				Text: renderFileAttachment(a),
			}},
		})
	}
	return out
}

// renderFileAttachment formats one file as the prompt-tag block the
// model is trained to recognise. We use the XML-ish wrapper
// `<file path="...">…</file>` so the model treats the content as
// a recognised file context.
func renderFileAttachment(a FileAttachment) string {
	var b strings.Builder
	fmt.Fprintf(&b,
		"This file was active before context compaction; here is the "+
			"current on-disk content. Use this as ground truth instead "+
			"of any version recalled from the summary.\n\n")
	fmt.Fprintf(&b, "<file path=%q size_bytes=%d", a.Path, a.SizeBytes)
	if a.WasTruncated {
		fmt.Fprintf(&b, " truncated=\"true\"")
	}
	b.WriteString(">\n")
	b.WriteString(a.Content)
	if !strings.HasSuffix(a.Content, "\n") {
		b.WriteByte('\n')
	}
	if a.WasTruncated {
		fmt.Fprintf(&b, "[… %d bytes truncated …]\n", a.SizeBytes-int64(len(a.Content)))
	}
	b.WriteString("</file>\n")
	return b.String()
}
