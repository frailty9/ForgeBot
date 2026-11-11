package qqbot

import (
	"net/url"
	"path"
	"strings"

	"github.com/soleapi/forgebot/internal/messaging"
)

// attachmentMedia 归一平台附件，不下载文件；明确的 MIME 优先于文件名。
func attachmentMedia(item attachment) (messaging.Media, bool) {
	address := strings.TrimSpace(item.URL)
	if address == "" {
		return messaging.Media{}, false
	}
	parsed, err := url.Parse(address)
	if err != nil {
		return messaging.Media{}, false
	}
	if parsed.Scheme == "" {
		if parsed.Host != "" {
			parsed.Scheme = "https"
		} else if !strings.HasPrefix(address, "/") {
			parsed, err = url.Parse("https://" + address)
		}
	}
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Scheme != "https" && parsed.Scheme != "http" {
		return messaging.Media{}, false
	}
	contentType := strings.TrimSpace(item.ContentType)
	if contentType == "" {
		switch strings.ToLower(path.Ext(item.Name)) {
		case ".jpg", ".jpeg":
			contentType = "image/jpeg"
		case ".png":
			contentType = "image/png"
		case ".gif":
			contentType = "image/gif"
		case ".webp":
			contentType = "image/webp"
		case ".bmp":
			contentType = "image/bmp"
		}
	}
	kind := messaging.MediaFile
	switch {
	case strings.HasPrefix(contentType, "image/"):
		kind = messaging.MediaImage
	case strings.HasPrefix(contentType, "video/"):
		kind = messaging.MediaVideo
	case contentType == "voice" || strings.HasPrefix(contentType, "audio/"):
		kind = messaging.MediaAudio
	}
	return messaging.Media{Kind: kind, URL: parsed.String(), MimeType: contentType, Name: item.Name}, true
}
