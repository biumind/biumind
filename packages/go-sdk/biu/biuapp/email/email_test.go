package email

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

const samplePlain = `From: alice@example.com
To: bob@example.com
Subject: Hello
Date: Mon, 02 Jan 2026 15:04:05 +0000
Message-Id: <abc@example.com>
Content-Type: text/plain

Hello there.`

const sampleMultipart = `From: alice@example.com
To: bob@example.com
Subject: =?UTF-8?B?5L2g5aW9?=
Content-Type: multipart/mixed; boundary="X"

--X
Content-Type: text/plain

Plain part.
--X
Content-Type: text/html

<p>HTML part.</p>
--X
Content-Type: application/pdf
Content-Disposition: attachment; filename="report.pdf"

PDF-bytes
--X--`

func TestParse_PlainText(t *testing.T) {
	out, err := Parse([]byte(samplePlain))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out.From != "alice@example.com" || out.To[0] != "bob@example.com" {
		t.Errorf("addresses: %+v", out)
	}
	if out.Subject != "Hello" || out.MessageID != "abc@example.com" {
		t.Errorf("headers: %+v", out)
	}
	if !strings.Contains(out.Text, "Hello there") {
		t.Errorf("body: %q", out.Text)
	}
	if out.Date.Year() != 2026 {
		t.Errorf("date not parsed: %v", out.Date)
	}
}

func TestParse_MultipartWithEncodedSubjectAndAttachment(t *testing.T) {
	out, err := Parse([]byte(sampleMultipart))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out.Subject != "你好" {
		t.Errorf("RFC2047 decode failed: %q", out.Subject)
	}
	if !strings.Contains(out.Text, "Plain part") {
		t.Errorf("text part: %q", out.Text)
	}
	if !strings.Contains(out.HTML, "HTML part") {
		t.Errorf("html part: %q", out.HTML)
	}
	if len(out.Attachments) != 1 || out.Attachments[0].Filename != "report.pdf" {
		t.Errorf("attachments: %+v", out.Attachments)
	}
}

func TestApp_InvokeWithBase64(t *testing.T) {
	a := New()
	body, _ := json.Marshal(map[string]any{
		"raw":    base64.StdEncoding.EncodeToString([]byte(samplePlain)),
		"base64": true,
	})
	out, err := a.Invoke(context.Background(), "parse", body)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	o := out.(*Output)
	if o.From != "alice@example.com" {
		t.Errorf("base64 path failed: %+v", o)
	}
}

func TestApp_RejectsEmptyRaw(t *testing.T) {
	a := New()
	_, err := a.Invoke(context.Background(), "parse", json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "missing raw") {
		t.Errorf("want missing-raw err, got %v", err)
	}
}
