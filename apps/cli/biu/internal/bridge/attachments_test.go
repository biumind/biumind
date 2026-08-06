package bridge

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
)

// uploadAttachmentReq 是个 helper：构造 multipart body + 发 POST 请求。
// 返回 response。调用方负责 Body.Close。
func uploadAttachmentReq(t *testing.T, ts *httptest.Server, sessionID, filename, mime string, content []byte) *http.Response {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	hdr := make(textproto.MIMEHeader)
	hdr.Set("Content-Disposition", `form-data; name="file"; filename="`+filename+`"`)
	hdr.Set("Content-Type", mime)
	part, err := mw.CreatePart(hdr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	mw.Close()

	req, _ := http.NewRequest("POST",
		ts.URL+"/v1/code/sessions/"+sessionID+"/attachments",
		&body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// attachmentTestServer 创建一个把 AttachmentDir 设到 t.TempDir() 的 server，
// 不污染 ~/.biu，且 cleanup 自动。
func attachmentTestServer(t *testing.T) (*httptest.Server, string, string) {
	t.Helper()
	tmp := t.TempDir()
	srv, err := NewServer(Options{
		AttachmentDir: tmp,
		AgentFactory: func(_ AgentExtras) (*biumindkit.Agent, error) {
			return biumindkit.New(biumindkit.Options{
				APIKey:              "sk-fake",
				LoadProjectMemory:   biumindkit.NoMemory,
				LoadProjectSettings: biumindkit.NoSettings,
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())

	resp, _ := http.Post(ts.URL+"/v1/code/sessions", "application/json", nil)
	var c struct{ ID string }
	_ = json.NewDecoder(resp.Body).Decode(&c)
	resp.Body.Close()
	return ts, c.ID, tmp
}

func TestAttachment_UploadJPEG(t *testing.T) {
	ts, sid, tmp := attachmentTestServer(t)
	defer ts.Close()

	// JPEG magic bytes 占位 —— 内容不重要，server 只看 mime 头
	content := []byte{0xFF, 0xD8, 0xFF, 0xE0, 'J', 'F', 'I', 'F', 0x00}
	resp := uploadAttachmentReq(t, ts, sid, "photo.jpg", "image/jpeg", content)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	var got struct {
		AttachmentID string `json:"attachment_id"`
		Size         int    `json:"size"`
		MimeType     string `json:"mime_type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.AttachmentID == "" {
		t.Errorf("missing attachment_id")
	}
	if got.Size != len(content) {
		t.Errorf("size=%d want %d", got.Size, len(content))
	}
	if got.MimeType != "image/jpeg" {
		t.Errorf("mime_type=%q", got.MimeType)
	}

	// 校验文件确实落了盘
	dst := filepath.Join(tmp, sid, "attachments", got.AttachmentID+".jpg")
	stat, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("file not on disk: %v", err)
	}
	if stat.Size() != int64(len(content)) {
		t.Errorf("on-disk size=%d want %d", stat.Size(), len(content))
	}
}

func TestAttachment_RejectsUnknownMIME(t *testing.T) {
	ts, sid, _ := attachmentTestServer(t)
	defer ts.Close()

	resp := uploadAttachmentReq(t, ts, sid, "evil.exe", "application/x-msdownload", []byte{0x4D, 0x5A})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d want 400", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "disallowed mime") {
		t.Errorf("error msg unexpected: %s", body)
	}
}

func TestAttachment_RejectsTooLarge(t *testing.T) {
	ts, sid, _ := attachmentTestServer(t)
	defer ts.Close()

	// 51 MB —— 超过 50 MB 上限
	big := make([]byte, 51*1024*1024)
	resp := uploadAttachmentReq(t, ts, sid, "big.png", "image/png", big)
	defer resp.Body.Close()
	// http.MaxBytesReader 让 ParseMultipartForm 报错 → 400 (我们的代码不区分
	// 413 vs 400)；接受任何 4xx
	if resp.StatusCode/100 != 4 {
		t.Errorf("status=%d want 4xx", resp.StatusCode)
	}
}

func TestAttachment_NoSession(t *testing.T) {
	ts, _, _ := attachmentTestServer(t)
	defer ts.Close()

	resp := uploadAttachmentReq(t, ts, "no-such-session", "f.txt", "text/plain", []byte("hi"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status=%d want 404", resp.StatusCode)
	}
}

func TestAttachment_MissingFileField(t *testing.T) {
	ts, sid, _ := attachmentTestServer(t)
	defer ts.Close()

	// 发个 multipart 不含 "file" 字段
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("not_file", "irrelevant")
	mw.Close()

	req, _ := http.NewRequest("POST",
		ts.URL+"/v1/code/sessions/"+sid+"/attachments", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d want 400", resp.StatusCode)
	}
}
