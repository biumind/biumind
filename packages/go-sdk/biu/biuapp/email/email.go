// Package email — RFC 822 → structured form, exposed as a BiuApp.
//
// This is the *parsing* half of the email pipeline. Inbound transport
// (IMAP poll / SMTP receive) lives in the Channels service when we add
// the email driver — the workbench app only needs to translate raw
// MIME into the canonical shape so agents can act on it.
//
// Action: `parse`
//
//	in:  {"raw": "<RFC 822 message bytes, base64 or plain>"}
//	out: {"from", "to[]", "cc[]", "subject", "date", "text", "html", "attachments[]"}
//
// We use net/mail + mime/multipart from the std lib, both of which are
// dependency-free and ship with Go.
package email

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
)

const Name = "email"

type App struct{}

func New() *App { return &App{} }

func (a *App) Manifest() biuapp.Manifest {
	return biuapp.Manifest{
		Name:        Name,
		Version:     "0.1.0",
		Description: "Parse raw RFC 822 email bodies into structured form",
		Author:      "BiuMind",
		Permissions: []string{}, // pure parser; no IO
		Actions: []biuapp.ActionSpec{
			{
				Name:        "parse",
				Description: "Parse a raw email (plain or base64) into a structured envelope",
			},
		},
	}
}

func (a *App) Init(ctx context.Context, deps biuapp.Deps) error { return nil }

type input struct {
	Raw      string `json:"raw"`
	IsBase64 bool   `json:"base64"`
}

type Output struct {
	From        string       `json:"from"`
	To          []string     `json:"to,omitempty"`
	Cc          []string     `json:"cc,omitempty"`
	Subject     string       `json:"subject,omitempty"`
	Date        time.Time    `json:"date,omitempty"`
	MessageID   string       `json:"message_id,omitempty"`
	Text        string       `json:"text,omitempty"`
	HTML        string       `json:"html,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

type Attachment struct {
	Filename string `json:"filename"`
	MimeType string `json:"mime_type"`
	Bytes    int    `json:"bytes"`
}

func (a *App) Invoke(ctx context.Context, action string, raw json.RawMessage) (any, error) {
	if action != "parse" {
		return nil, fmt.Errorf("email: unknown action %q", action)
	}
	var in input
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("email: bad input: %w", err)
	}
	if in.Raw == "" {
		return nil, errors.New("email: missing raw")
	}
	body := []byte(in.Raw)
	if in.IsBase64 {
		dec, err := base64.StdEncoding.DecodeString(in.Raw)
		if err != nil {
			return nil, fmt.Errorf("email: base64: %w", err)
		}
		body = dec
	}
	return Parse(body)
}

// Parse is exported for direct use by adapters that already have the
// raw bytes (e.g. the future Email channel driver).
func Parse(raw []byte) (*Output, error) {
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("email: read: %w", err)
	}
	out := &Output{}
	out.From = msg.Header.Get("From")
	out.To = splitAddresses(msg.Header.Get("To"))
	out.Cc = splitAddresses(msg.Header.Get("Cc"))
	out.MessageID = strings.Trim(msg.Header.Get("Message-Id"), "<>")
	if subject, err := decodeHeader(msg.Header.Get("Subject")); err == nil {
		out.Subject = subject
	}
	if d, err := mail.ParseDate(msg.Header.Get("Date")); err == nil {
		out.Date = d.UTC()
	}

	// Body — multipart vs plain.
	contentType := msg.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/plain"
	}
	mt, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		mt = "text/plain"
	}
	if strings.HasPrefix(mt, "multipart/") {
		mr := multipart.NewReader(msg.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return out, fmt.Errorf("email: multipart: %w", err)
			}
			data, err := io.ReadAll(io.LimitReader(part, 5<<20))
			if err != nil {
				return out, err
			}
			pt, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
			disp := part.Header.Get("Content-Disposition")
			fname := part.FileName()
			switch {
			case strings.HasPrefix(pt, "text/plain") && fname == "" && !strings.Contains(disp, "attachment"):
				out.Text += string(data)
			case strings.HasPrefix(pt, "text/html") && fname == "":
				out.HTML += string(data)
			default:
				out.Attachments = append(out.Attachments, Attachment{
					Filename: fname,
					MimeType: pt,
					Bytes:    len(data),
				})
			}
		}
	} else {
		data, err := io.ReadAll(io.LimitReader(msg.Body, 5<<20))
		if err != nil {
			return out, err
		}
		if mt == "text/html" {
			out.HTML = string(data)
		} else {
			out.Text = string(data)
		}
	}
	return out, nil
}

func splitAddresses(header string) []string {
	if header == "" {
		return nil
	}
	addrs, err := mail.ParseAddressList(header)
	if err != nil || len(addrs) == 0 {
		return nil
	}
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.Address)
	}
	return out
}

// decodeHeader handles RFC 2047 encoded-words like
// "=?UTF-8?B?5L2g5aW9?=" → "你好".
func decodeHeader(s string) (string, error) {
	dec := mime.WordDecoder{}
	return dec.DecodeHeader(s)
}
