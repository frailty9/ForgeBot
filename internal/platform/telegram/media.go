package telegram

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/operation"
)

// 每条消息最多一张照片和一份图片文档，连同引用消息最多 4 张；单图上限保证总量不超过 20MiB。
const maxImageBytes = 5 << 20

type photoSize struct {
	FileID   string `json:"file_id"`
	Width    int64  `json:"width"`
	Height   int64  `json:"height"`
	FileSize int64  `json:"file_size"`
}

type fileRef struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
}

// pendingImage 只保存 file_id，下载在后台工作者里进行，不阻塞接收循环。
type pendingImage struct {
	fileID, name string
}

var imageTypes = map[string]string{".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".gif": "image/gif", ".webp": "image/webp", ".bmp": "image/bmp"}

func supportedImageType(mime string) bool {
	for _, known := range imageTypes {
		if mime == known {
			return true
		}
	}
	return false
}

// mediaOf 把附件分成待下载的图片与只带元数据的其他附件。照片的多个尺寸是同一张图，
// 不假定数组有序：选像素面积最大、且已知大小不超过单图上限的版本；全部已知超限时报错，
// 不能当作无图消息继续问答。
func mediaOf(m *message) ([]pendingImage, []messaging.Media, error) {
	var images []pendingImage
	var others []messaging.Media
	if len(m.Photo) > 0 {
		best := -1
		for index, size := range m.Photo {
			if size.FileID == "" || size.FileSize > maxImageBytes {
				continue
			}
			if best < 0 || size.Width*size.Height > m.Photo[best].Width*m.Photo[best].Height {
				best = index
			}
		}
		if best < 0 {
			return nil, nil, operation.New(operation.Invalid, "Telegram photo exceeds the 5MiB image limit")
		}
		images = append(images, pendingImage{fileID: m.Photo[best].FileID, name: "photo"})
	}
	if d := m.Document; d != nil && d.FileID != "" {
		mime := d.MimeType
		if mime == "" {
			mime = imageTypes[strings.ToLower(path.Ext(d.FileName))]
		}
		if supportedImageType(mime) {
			if d.FileSize > maxImageBytes {
				return nil, nil, operation.New(operation.Invalid, "Telegram image document exceeds the 5MiB image limit")
			}
			images = append(images, pendingImage{fileID: d.FileID, name: d.FileName})
		} else {
			others = append(others, messaging.Media{Kind: messaging.MediaFile, Name: d.FileName, MimeType: d.MimeType})
		}
	}
	for _, item := range []struct {
		ref  *fileRef
		kind messaging.MediaKind
		name string
	}{{m.Video, messaging.MediaVideo, "video"}, {m.Animation, messaging.MediaVideo, "animation"},
		{m.Audio, messaging.MediaAudio, "audio"}, {m.Voice, messaging.MediaAudio, "voice"}, {m.Sticker, messaging.MediaFile, "sticker"}} {
		if item.ref == nil || item.ref.FileID == "" {
			continue
		}
		name := item.ref.FileName
		if name == "" {
			name = item.name
		}
		others = append(others, messaging.Media{Kind: item.kind, Name: name, MimeType: item.ref.MimeType})
	}
	return images, others, nil
}

// downloadImages 逐张下载并按文件头确认真实类型；任一张失败或超限都停止整条消息，
// 不丢图后让模型只看文字。下载地址含 Bot Token，只设置 Data，不写入 Media.URL。
func (a *Adapter) downloadImages(ctx context.Context, pending []pendingImage) ([]messaging.Media, error) {
	media := make([]messaging.Media, 0, len(pending))
	for _, item := range pending {
		data, err := a.download(ctx, item.fileID)
		if err != nil {
			return nil, err
		}
		mime := http.DetectContentType(data)
		if !supportedImageType(mime) {
			return nil, operation.New(operation.Unsupported, "Telegram image type could not be confirmed")
		}
		media = append(media, messaging.Media{Kind: messaging.MediaImage, MimeType: mime, Data: data, Name: item.name})
	}
	return media, nil
}

// validFilePath 只接受 getFile 返回的相对路径，不能被解释成外部地址或本地文件。
func validFilePath(value string) bool {
	return value != "" && len(value) <= 1024 && !strings.HasPrefix(value, "/") &&
		!strings.Contains(value, "..") && !strings.ContainsAny(value, "\\?#:\r\n")
}

func (a *Adapter) download(ctx context.Context, fileID string) ([]byte, error) {
	var file struct {
		Path string `json:"file_path"`
		Size int64  `json:"file_size"`
	}
	if err := a.call(ctx, "getFile", map[string]any{"file_id": fileID}, &file, a.cfg.Timeout); err != nil {
		return nil, err
	}
	if file.Size > maxImageBytes {
		return nil, operation.New(operation.Invalid, "Telegram image exceeds the 5MiB image limit")
	}
	if !validFilePath(file.Path) {
		return nil, operation.New(operation.Invalid, "invalid Telegram file path")
	}
	segments := strings.Split(file.Path, "/")
	for index := range segments {
		segments[index] = url.PathEscape(segments[index])
	}
	ctx, cancel := a.requestContext(ctx, a.cfg.Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, a.apiBase+"/file/bot"+a.cfg.Token+"/"+strings.Join(segments, "/"), nil)
	if err != nil {
		return nil, operation.New(operation.Invalid, "invalid Telegram file address")
	}
	response, err := a.client.Do(request)
	if err != nil {
		return nil, operation.Wrap("telegram.download", operation.Unavailable, operation.NotStarted, safeTransportError(err))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		code := operation.HTTPCode(response.StatusCode)
		return nil, operation.Wrap("telegram.download", code, operation.NotStarted, operation.New(code, "Telegram file download rejected"))
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxImageBytes+1))
	if err != nil {
		return nil, operation.Wrap("telegram.download", operation.Unavailable, operation.NotStarted, safeTransportError(err))
	}
	if len(data) > maxImageBytes {
		return nil, operation.Wrap("telegram.download", operation.Invalid, operation.NotStarted, errors.New("Telegram image exceeds the 5MiB image limit"))
	}
	return data, nil
}
