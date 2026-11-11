package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/operation"
	"github.com/soleapi/forgebot/internal/platform"
)

var pngHeader = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

// fileTransport 模拟 getFile 与文件下载；files 以 file_id 为键给出下载内容。
func fileTransport(t *testing.T, files map[string][]byte, downloads *[]string, mu *sync.Mutex) roundTrip {
	t.Helper()
	return func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/file/bot") {
			mu.Lock()
			*downloads = append(*downloads, r.URL.Path)
			mu.Unlock()
			id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			data, ok := files[id]
			if !ok {
				return response(404, "missing"), nil
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header)}, nil
		}
		switch r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:] {
		case "getFile":
			var body struct {
				FileID string `json:"file_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			return response(200, `{"ok":true,"result":{"file_path":"photos/`+body.FileID+`"}}`), nil
		case "getChatAdministrators":
			return response(200, `{"ok":true,"result":[]}`), nil
		}
		t.Errorf("unexpected call %s", r.URL.Path)
		return nil, errors.New("unexpected")
	}
}

func TestPhotoSelectionUsesLargestAllowedAreaRegardlessOfOrder(t *testing.T) {
	m := testMessage()
	m.Photo = []photoSize{
		{FileID: "big", Width: 2000, Height: 2000, FileSize: maxImageBytes + 1},
		{FileID: "small", Width: 90, Height: 90, FileSize: 1000},
		{FileID: "medium", Width: 800, Height: 800},
	}
	images, _, err := mediaOf(&m)
	if err != nil || len(images) != 1 || images[0].fileID != "medium" {
		t.Fatal(images, err)
	}
	m.Photo = []photoSize{{FileID: "huge", Width: 1, Height: 1, FileSize: maxImageBytes + 1}}
	if _, _, err := mediaOf(&m); operation.Describe(err).Code != operation.Invalid {
		t.Fatal("all-oversized photo became a text message", err)
	}
	m = testMessage()
	m.Document = &fileRef{FileID: "doc", FileName: "scan.PNG"}
	images, others, err := mediaOf(&m)
	if err != nil || len(images) != 1 || len(others) != 0 {
		t.Fatal("image document by extension not recognized", images, others, err)
	}
	m.Document = &fileRef{FileID: "doc", FileName: "report.pdf", MimeType: "application/pdf"}
	images, others, err = mediaOf(&m)
	if err != nil || len(images) != 0 || len(others) != 1 || others[0].Kind != messaging.MediaFile {
		t.Fatal("non-image document downloaded", images, others, err)
	}
}

func TestImagesDownloadInWorkerWithDetectedTypeAndNoTokenLeak(t *testing.T) {
	var logs bytes.Buffer
	a := testAdapter(t)
	a.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	var mu sync.Mutex
	var downloads []string
	a.client.Transport = fileTransport(t, map[string][]byte{"current": append(append([]byte(nil), pngHeader...), 1, 2, 3), "quoted": []byte("GIF89a....")}, &downloads, &mu)
	m := testMessage()
	m.Text, m.Caption = "", "what is this @ForgeBot"
	m.CaptionEntities = []entity{{Type: "mention", Offset: 13, Length: 9}}
	m.Photo = []photoSize{{FileID: "current", Width: 10, Height: 10}}
	reply := testMessage()
	reply.ID = 40
	reply.Photo = []photoSize{{FileID: "quoted", Width: 10, Height: 10}}
	m.Reply = &reply
	var got messaging.InboundMessage
	err := a.handle(context.Background(), handlerFunc(func(_ context.Context, in messaging.InboundMessage) (platform.HandleResult, error) {
		got = in
		return platform.HandleResult{}, nil
	}), update{ID: 1, Message: &m})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Media) != 1 || got.Media[0].MimeType != "image/png" || got.Media[0].URL != "" || len(got.Media[0].Data) == 0 {
		t.Fatal("current image", got.Media)
	}
	if got.Quoted == nil || len(got.Quoted.Media) != 1 || got.Quoted.Media[0].MimeType != "image/gif" || got.Text != "what is this" {
		t.Fatal("quoted image", got.Quoted, got.Text)
	}
	if len(downloads) != 2 {
		t.Fatal(downloads)
	}
	data, _ := json.Marshal(got)
	if strings.Contains(string(data), a.cfg.Token) || strings.Contains(logs.String(), a.cfg.Token) {
		t.Fatal("token leaked into message or logs")
	}
}

func TestImageFailuresStopWholeMessage(t *testing.T) {
	for _, mode := range []string{"unknown type", "oversized", "missing", "declared size"} {
		t.Run(mode, func(t *testing.T) {
			a := testAdapter(t)
			var mu sync.Mutex
			var downloads []string
			files := map[string][]byte{"img": append(append([]byte(nil), pngHeader...), 0)}
			switch mode {
			case "unknown type":
				files["img"] = []byte("plain text, not an image")
			case "oversized":
				files["img"] = append(append([]byte(nil), pngHeader...), bytes.Repeat([]byte{0}, maxImageBytes)...)
			case "missing":
				delete(files, "img")
			}
			a.client.Transport = fileTransport(t, files, &downloads, &mu)
			if mode == "declared size" {
				a.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
					if strings.HasSuffix(r.URL.Path, "/getFile") {
						return response(200, `{"ok":true,"result":{"file_path":"p/img","file_size":99999999}}`), nil
					}
					t.Error("downloaded a file declared oversized", r.URL.Path)
					return nil, errors.New("unexpected")
				})
			}
			m := testMessage()
			m.Photo = []photoSize{{FileID: "img", Width: 1, Height: 1}}
			handled := false
			err := a.handle(context.Background(), handlerFunc(func(context.Context, messaging.InboundMessage) (platform.HandleResult, error) {
				handled = true
				return platform.HandleResult{}, nil
			}), update{ID: 1, Message: &m})
			if err == nil || handled {
				t.Fatal("image failure fell back to text-only handling", err, handled)
			}
		})
	}
}

// 一条消息最多一张照片和一份图片文档，连同同群引用最多 4 张，因此不另设张数上限。
func TestCurrentAndQuotedImagesAreAllDelivered(t *testing.T) {
	a := testAdapter(t)
	var mu sync.Mutex
	var downloads []string
	img := append(append([]byte(nil), pngHeader...), 0)
	a.client.Transport = fileTransport(t, map[string][]byte{"p": img, "d": img, "q": img, "qd": img}, &downloads, &mu)
	m := testMessage()
	m.Photo = []photoSize{{FileID: "p", Width: 1, Height: 1}}
	m.Document = &fileRef{FileID: "d", MimeType: "image/png"}
	reply := testMessage()
	reply.ID = 40
	reply.Photo = []photoSize{{FileID: "q", Width: 1, Height: 1}}
	reply.Document = &fileRef{FileID: "qd", MimeType: "image/png"}
	m.Reply = &reply
	var got messaging.InboundMessage
	if err := a.handle(context.Background(), handlerFunc(func(_ context.Context, in messaging.InboundMessage) (platform.HandleResult, error) {
		got = in
		return platform.HandleResult{}, nil
	}), update{ID: 1, Message: &m}); err != nil {
		t.Fatal(err)
	}
	if len(got.Media) != 2 || got.Quoted == nil || len(got.Quoted.Media) != 2 || len(downloads) != 4 {
		t.Fatal(len(got.Media), got.Quoted, downloads)
	}
}

func TestFilePathMustStayUnderOfficialHost(t *testing.T) {
	for _, path := range []string{"", "/etc/passwd", "../x", "https://evil/x", "a?b", `a\b`} {
		if validFilePath(path) {
			t.Fatal("unsafe file path accepted", path)
		}
	}
	if !validFilePath("photos/file_1.jpg") {
		t.Fatal("valid file path rejected")
	}
}
