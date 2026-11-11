package qqbot

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/soleapi/forgebot/internal/messaging"
)

type payload struct {
	Op   int             `json:"op"`
	Type string          `json:"t"`
	Data json.RawMessage `json:"d"`
}
type user struct {
	ID           string `json:"id"`
	MemberOpenID string `json:"member_openid"`
	Name         string `json:"username"`
	Role         string `json:"member_role"`
	Bot          bool   `json:"bot"`
}
type mention struct {
	ID           string `json:"id"`
	MemberOpenID string `json:"member_openid"`
	IsYou        bool   `json:"is_you"`
}
type attachment struct {
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	Name        string `json:"filename"`
}
type element struct {
	Index   string `json:"msg_idx"`
	Content string `json:"content"`
}
type groupMessage struct {
	ID        string    `json:"id"`
	Group     string    `json:"group_openid"`
	Content   string    `json:"content"`
	Timestamp string    `json:"timestamp"`
	Author    user      `json:"author"`
	Mentions  []mention `json:"mentions"`
	Scene     struct {
		Ext []string `json:"ext"`
	} `json:"message_scene"`
	Attachments []attachment `json:"attachments"`
	Elements    []element    `json:"msg_elements"`
}

func (a *Adapter) validTimestamp(timestamp string) bool {
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	age := a.now().Sub(time.Unix(seconds, 0))
	return age >= -time.Minute && age <= 5*time.Minute
}

// validChallengeToken 将未签名挑战限制为令牌文本，不能借此为业务事件 JSON 签名。
func validChallengeToken(token string) bool {
	if len(token) == 0 || len(token) > 4096 {
		return false
	}
	for _, ch := range token {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || strings.ContainsRune("-_+/=", ch) {
			continue
		}
		return false
	}
	return true
}

func (a *Adapter) webhook(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	if r.Header.Get("X-Bot-Appid") != a.cfg.AppID {
		http.Error(w, "invalid app", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, a.cfg.RequestLimit))
	if err != nil {
		http.Error(w, "invalid payload size", http.StatusRequestEntityTooLarge)
		return
	}
	var envelope payload
	if json.Unmarshal(body, &envelope) != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	// op=13 是开放平台验证回调地址的挑战，可不带签名头。
	// 只签有界令牌，不分发事件；业务事件仍必须通过下面的原始体验签。
	if envelope.Op == 13 {
		var challenge struct {
			PlainToken string `json:"plain_token"`
			Timestamp  string `json:"event_ts"`
		}
		if json.Unmarshal(envelope.Data, &challenge) != nil || !validChallengeToken(challenge.PlainToken) || !a.validTimestamp(challenge.Timestamp) {
			http.Error(w, "invalid challenge", http.StatusBadRequest)
			return
		}
		signature := hex.EncodeToString(ed25519.Sign(a.private, []byte(challenge.Timestamp+challenge.PlainToken)))
		_ = json.NewEncoder(w).Encode(map[string]string{"plain_token": challenge.PlainToken, "signature": signature})
		return
	}
	timestamp := r.Header.Get("X-Signature-Timestamp")
	signature, err := hex.DecodeString(r.Header.Get("X-Signature-Ed25519"))
	if err != nil || len(signature) != ed25519.SignatureSize || !a.validTimestamp(timestamp) || !ed25519.Verify(a.private.Public().(ed25519.PublicKey), append([]byte(timestamp), body...), signature) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	if envelope.Op == 1 {
		_, _ = w.Write([]byte(`{"op":11,"d":` + string(envelope.Data) + `}`))
		return
	}
	if envelope.Op != 0 || (envelope.Type != "GROUP_AT_MESSAGE_CREATE" && envelope.Type != "GROUP_MESSAGE_CREATE") {
		dispatchACK(w, true)
		return
	}
	var data groupMessage
	if json.Unmarshal(envelope.Data, &data) != nil || data.ID == "" || data.Group == "" || len(data.ID) > 4096 {
		dispatchACK(w, false)
		return
	}
	binding, bound := a.cfg.Groups[data.Group]
	if !bound || data.Author.Bot {
		dispatchACK(w, true)
		return
	}
	message, meta, ok := a.inbound(data, binding, envelope.Type == "GROUP_AT_MESSAGE_CREATE")
	if !ok {
		dispatchACK(w, false)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.accepting {
		dispatchACK(w, false)
		return
	}
	now := a.now()
	a.pruneLocked(now)
	key := messageKey(data.Group, data.ID)
	if until, seen := a.replays[key]; seen && until.After(now) {
		dispatchACK(w, true)
		return
	}
	if len(a.replays) >= a.cfg.ReplayMax {
		dispatchACK(w, false)
		return
	}
	// 引用索引由平台给出，与消息 ID 分开。只使用同群已见过的对应关系。
	ref := ""
	for _, entry := range data.Scene.Ext {
		if strings.HasPrefix(entry, "ref_msg_idx=") {
			ref = strings.TrimPrefix(entry, "ref_msg_idx=")
			break
		}
	}
	if ref != "" {
		for _, known := range a.messages {
			if known.group == data.Group && known.reference == ref {
				message.ReplyToMessageID = known.id
				for _, element := range data.Elements {
					if element.Index == ref && element.Content != "" {
						message.Quoted = &messaging.QuotedMessage{MessageID: known.id, Text: element.Content}
						break
					}
				}
				break
			}
		}
	}
	select {
	case a.queue <- message:
		a.replays[key] = now.Add(a.cfg.ReplayTTL)
		a.rememberLocked(meta)
		dispatchACK(w, true)
	default:
		dispatchACK(w, false)
	}
}

func dispatchACK(w http.ResponseWriter, success bool) {
	value := 1
	if success {
		value = 0
	}
	_ = json.NewEncoder(w).Encode(struct {
		Op   int `json:"op"`
		Data int `json:"d"`
	}{12, value})
}

func (a *Adapter) inbound(data groupMessage, binding Group, mentioned bool) (messaging.InboundMessage, messageMeta, bool) {
	userID := data.Author.MemberOpenID
	if userID == "" {
		userID = data.Author.ID
	}
	at, err := time.Parse(time.RFC3339Nano, data.Timestamp)
	if err != nil || userID == "" {
		return messaging.InboundMessage{}, messageMeta{}, false
	}
	role := "member"
	if data.Author.Role == "owner" || data.Author.Role == "admin" {
		role = data.Author.Role
	}
	text := strings.TrimSpace(data.Content)
	// 在同群消息 ID 去重前归一明确 @，避免全量事件先到时吞掉专用 @ 事件。
	// 只信平台的 is_you，不按正文标签、昵称或其他 Bot 身份猜测当前机器人。
	for _, target := range data.Mentions {
		if !target.IsYou {
			continue
		}
		mentioned = true
		for _, id := range []string{target.ID, target.MemberOpenID} {
			if id != "" {
				text = strings.ReplaceAll(text, "<@"+id+">", "")
				text = strings.ReplaceAll(text, "<@!"+id+">", "")
			}
		}
	}
	text = strings.TrimSpace(text)
	message := messaging.InboundMessage{ConversationID: binding.ConversationID, UserID: userID, UserName: data.Author.Name, MessageID: data.ID, Text: text,
		Mentioned: mentioned, Command: strings.HasPrefix(text, "/"), PlatformRole: role, At: at, PromptSuffix: strings.TrimSpace(a.cfg.PromptSuffix + "\n" + binding.PromptSuffix)}
	for _, item := range data.Attachments {
		if !strings.HasPrefix(item.URL, "https://") && !strings.HasPrefix(item.URL, "http://") {
			continue
		}
		kind := messaging.MediaFile
		switch {
		case strings.HasPrefix(item.ContentType, "image/"):
			kind = messaging.MediaImage
		case strings.HasPrefix(item.ContentType, "video/"):
			kind = messaging.MediaVideo
		case item.ContentType == "voice" || strings.HasPrefix(item.ContentType, "audio/"):
			kind = messaging.MediaAudio
		}
		message.Media = append(message.Media, messaging.Media{Kind: kind, URL: item.URL, MimeType: item.ContentType, Name: item.Name})
		if len(message.Media) == 16 {
			break
		}
	}
	meta := messageMeta{group: data.Group, id: data.ID, at: at}
	for _, entry := range data.Scene.Ext {
		if strings.HasPrefix(entry, "msg_idx=") {
			meta.reference = strings.TrimPrefix(entry, "msg_idx=")
			break
		}
	}
	return message, meta, true
}
