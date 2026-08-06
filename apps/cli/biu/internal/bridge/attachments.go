// 附件上传：HTTP multipart POST → 落盘 → 返回 attachment_id。
//
// 客户端先 upload 文件，server 返回 attachment_id；后续的 user_message 通过
// `biumind_attachment_ids: []` 引用 —— 把 ID 列表塞进 prompt 或 message
// metadata，由 brain / runtime 在构造 Anthropic content blocks 时取出来再
// reference。
//
// S2-4 范围：只做"保存到磁盘 + 返回 ID"。引用语义留给 user_message 通道
// 落地（S4 brain 集成时一起实化）。
//
// 落盘路径：`<AttachmentDir>/<session_id>/<attachment_id><ext>`，AttachmentDir
// 默认 `~/.biu/sessions`。删除 session 时清理（delete 路径自带）。
//
// 限制：50MB / 单文件；MIME 白名单（图片 + PDF + 纯文本）；总盘面没限制 ——
// 当前是 dev tooling，单机用，磁盘填满前用户应该手动清理。生产部署需要
// 加 quota（不在 S2-4 范围）。

package bridge

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// maxAttachmentSize 是单个附件上传上限。50 MB 跟 Anthropic Files API
// 上限一致，避免上传成功但下游拒绝。
const maxAttachmentSize = 50 * 1024 * 1024

// allowedMIMETypes 是上传白名单。Anthropic vision 支持 jpeg/png/gif/webp；
// PDF 部分模型支持；text/plain 留给小型代码片段附件。其他类型拒绝
// （包括 zip / executable 等潜在攻击面）。
//
// 用 map 而不是 slice 是为了 O(1) 查询。
var allowedMIMETypes = map[string]string{
	"image/jpeg":      ".jpg",
	"image/png":       ".png",
	"image/gif":       ".gif",
	"image/webp":      ".webp",
	"application/pdf": ".pdf",
	"text/plain":      ".txt",
}

// uploadAttachment handles `POST /v1/code/sessions/:id/attachments`。
// multipart/form-data，字段名 "file"。
//
// 错误返回 JSON {error: msg}：
//   - 404: session 不存在
//   - 400: 缺 file 字段 / 类型不允许 / 解析失败
//   - 413: 超过大小限制（http.MaxBytesReader 自动写）
//   - 500: 落盘失败
func (s *Server) uploadAttachment(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.lookup(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "no such session")
		return
	}

	// 双层限制：MaxBytesReader 包整个 body（防 multipart header 也大），
	// ParseMultipartForm 限制单字段（基本同效，保险起见）。超出会 413。
	r.Body = http.MaxBytesReader(w, r.Body, maxAttachmentSize)
	if err := r.ParseMultipartForm(maxAttachmentSize); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("parse multipart: %v", err))
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "missing 'file' field")
		return
	}
	defer file.Close()

	// 信任 Content-Type 头但只信白名单内的；客户端伪造头会被服务端 sniff
	// 时纠正——但 detect 也可以伪造。生产实现要 magic-byte sniff（如
	// http.DetectContentType），S2-4 暂时 trust client header。
	mime := header.Header.Get("Content-Type")
	ext, ok := allowedMIMETypes[mime]
	if !ok {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("disallowed mime type %q", mime))
		return
	}

	attachmentID, err := newAttachmentID()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Sprintf("gen id: %v", err))
		return
	}

	dir, err := s.attachmentDir(rec.id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Sprintf("dir: %v", err))
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Sprintf("mkdir: %v", err))
		return
	}

	dst := filepath.Join(dir, attachmentID+ext)
	out, err := os.Create(dst)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Sprintf("create: %v", err))
		return
	}
	defer out.Close()

	written, err := io.Copy(out, file)
	if err != nil {
		_ = os.Remove(dst) // 不留半截文件
		writeErr(w, http.StatusInternalServerError, fmt.Sprintf("write: %v", err))
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"attachment_id": attachmentID,
		"size":          written,
		"mime_type":     mime,
	})
}

// attachmentDir 返回 `<root>/<session_id>` —— root 取自 Options.AttachmentDir，
// 或 ~/.biu/sessions 兜底。Returns error when HOME 解析不出来（极少见）。
func (s *Server) attachmentDir(sessionID string) (string, error) {
	root := s.opt.AttachmentDir
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("home dir: %w", err)
		}
		root = filepath.Join(home, ".biu", "sessions")
	}
	if sessionID == "" {
		return "", errors.New("empty session id")
	}
	return filepath.Join(root, sessionID, "attachments"), nil
}

// newAttachmentID 生成 16-byte hex ID。跟 session id 同格式，无歧义。
func newAttachmentID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
