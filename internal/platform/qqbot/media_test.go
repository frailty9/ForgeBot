package qqbot

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/messaging"
)

func eventWithAttachments(t *testing.T, a *Adapter, attachments []attachment) []byte {
	t.Helper()
	data, err := json.Marshal(groupMessage{
		ID: "attachment", Group: "group", Timestamp: a.now().Format(time.RFC3339Nano),
		Author: user{MemberOpenID: "user"}, Attachments: attachments,
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(payload{Op: 0, Type: "GROUP_AT_MESSAGE_CREATE", Data: data})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestAttachmentURLsAndMediaTypes(t *testing.T) {
	for _, tc := range []struct {
		name, address, contentType, filename string
		wantURL, wantMIME                    string
		wantKind                             messaging.MediaKind
	}{
		{"https", "https://cdn.example/a.png?key=x%2Fy", "image/png", "a.png", "https://cdn.example/a.png?key=x%2Fy", "image/png", messaging.MediaImage},
		{"http", "http://cdn.example/a.jpg", "image/jpeg", "a.jpg", "http://cdn.example/a.jpg", "image/jpeg", messaging.MediaImage},
		{"bare domain", "multimedia.nt.qq.com.cn/download?a=1", "image/png", "a.png", "https://multimedia.nt.qq.com.cn/download?a=1", "image/png", messaging.MediaImage},
		{"protocol relative", "//gchat.qpic.cn/c", "image/jpeg", "a.jpg", "https://gchat.qpic.cn/c", "image/jpeg", messaging.MediaImage},
		{"filename uppercase", "//cdn.example/download", "", "a.JPG", "https://cdn.example/download", "image/jpeg", messaging.MediaImage},
		{"filename jpeg", "https://cdn.example/download", "", "a.jpeg", "https://cdn.example/download", "image/jpeg", messaging.MediaImage},
		{"filename png", "https://cdn.example/download", "", "a.png", "https://cdn.example/download", "image/png", messaging.MediaImage},
		{"filename gif", "https://cdn.example/download", "", "a.gif", "https://cdn.example/download", "image/gif", messaging.MediaImage},
		{"filename webp", "https://cdn.example/download", "", "a.webp", "https://cdn.example/download", "image/webp", messaging.MediaImage},
		{"filename bmp", "https://cdn.example/download", "", "a.bmp", "https://cdn.example/download", "image/bmp", messaging.MediaImage},
		{"explicit MIME wins", "https://cdn.example/a.png", "application/octet-stream", "a.png", "https://cdn.example/a.png", "application/octet-stream", messaging.MediaFile},
		{"unknown file", "https://cdn.example/a.zip", "", "a.zip", "https://cdn.example/a.zip", "", messaging.MediaFile},
		{"video", "//cdn.example/a.mp4", "video/mp4", "a.mp4", "https://cdn.example/a.mp4", "video/mp4", messaging.MediaVideo},
		{"audio", "//cdn.example/a.ogg", "audio/ogg", "a.ogg", "https://cdn.example/a.ogg", "audio/ogg", messaging.MediaAudio},
		{"voice", "//cdn.example/voice", "voice", "voice", "https://cdn.example/voice", "voice", messaging.MediaAudio},
		{name: "empty"},
		{name: "missing host", address: "https:///a.png"},
		{name: "relative path", address: "/images/a.png"},
		{name: "invalid host", address: "https://bad host/a.png"},
		{name: "invalid escape", address: "https://cdn.example/%zz"},
		{name: "userinfo", address: "https://user:password@cdn.example/a.png"},
		{name: "other protocol", address: "ftp://cdn.example/a.png"},
		{name: "opaque protocol", address: "file:/tmp/a.png"},
		{name: "inline data", address: "data:image/png;base64,AAAA"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testAdapter(t)
			item := attachment{URL: tc.address, ContentType: tc.contentType, Name: tc.filename}
			dispatch(t, a, eventWithAttachments(t, a, []attachment{item}), 0)
			message := <-a.queue
			if tc.wantURL == "" {
				if len(message.Media) != 0 {
					t.Fatalf("invalid attachment retained: %+v", message.Media)
				}
				return
			}
			if len(message.Media) != 1 {
				t.Fatalf("media=%+v", message.Media)
			}
			media := message.Media[0]
			if media.URL != tc.wantURL || media.MimeType != tc.wantMIME || media.Kind != tc.wantKind || media.Name != tc.filename || len(media.Data) != 0 {
				t.Fatalf("media=%+v", media)
			}
		})
	}
}

func TestAttachmentsKeepLimitAfterSkippingInvalidURLs(t *testing.T) {
	a := testAdapter(t)
	items := []attachment{{URL: "file:/tmp/invalid.png"}}
	for range 17 {
		items = append(items, attachment{URL: "//cdn.example/a.png", ContentType: "image/png"})
	}
	dispatch(t, a, eventWithAttachments(t, a, items), 0)
	message := <-a.queue
	if len(message.Media) != 16 {
		t.Fatalf("media count=%d, want 16", len(message.Media))
	}
}
