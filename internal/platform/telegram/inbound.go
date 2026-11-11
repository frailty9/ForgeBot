package telegram

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/operation"
	"github.com/soleapi/forgebot/internal/platform"
)

type user struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

type chat struct {
	ID      int64  `json:"id"`
	Type    string `json:"type"`
	IsForum bool   `json:"is_forum"`
}

func (c chat) supported() bool {
	return c.Type == "group" || c.Type == "supergroup" && !c.IsForum
}

type entity struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
	User   *user  `json:"user"`
}

type message struct {
	ID              int64             `json:"message_id"`
	Date            int64             `json:"date"`
	Chat            chat              `json:"chat"`
	From            user              `json:"from"`
	SenderChat      *chat             `json:"sender_chat"`
	IsTopic         bool              `json:"is_topic_message"`
	ThreadID        int64             `json:"message_thread_id"`
	Text            string            `json:"text"`
	Caption         string            `json:"caption"`
	Entities        []entity          `json:"entities"`
	CaptionEntities []entity          `json:"caption_entities"`
	Reply           *message          `json:"reply_to_message"`
	NewMembers      []user            `json:"new_chat_members"`
	MigrateTo       int64             `json:"migrate_to_chat_id"`
	MigrateFrom     int64             `json:"migrate_from_chat_id"`
	Photo           []json.RawMessage `json:"photo"`
	Document        json.RawMessage   `json:"document"`
	Video           json.RawMessage   `json:"video"`
	Audio           json.RawMessage   `json:"audio"`
	Voice           json.RawMessage   `json:"voice"`
	Animation       json.RawMessage   `json:"animation"`
	Sticker         json.RawMessage   `json:"sticker"`
}

func (m message) hasMedia() bool {
	if len(m.Photo) != 0 {
		return true
	}
	for _, value := range []json.RawMessage{m.Document, m.Video, m.Audio, m.Voice, m.Animation, m.Sticker} {
		if len(value) > 0 && string(value) != "null" {
			return true
		}
	}
	return false
}

type update struct {
	ID         int64    `json:"update_id"`
	Message    *message `json:"message"`
	Membership *struct {
		Chat chat `json:"chat"`
	} `json:"my_chat_member"`
}

type textEdit struct {
	start, end int
	value      []uint16
}

// normalizeText 的所有范围基于原始 UTF-16；修改前先判断命令目标，再逆序应用编辑。
func normalizeText(text string, entities []entity, self user) (string, bool, bool) {
	units := utf16.Encode([]rune(text))
	boundary := func(index int) bool {
		return index == 0 || index == len(units) || !(units[index-1] >= 0xD800 && units[index-1] <= 0xDBFF && units[index] >= 0xDC00 && units[index] <= 0xDFFF)
	}
	var edits []textEdit
	mentioned := false
	for _, part := range entities {
		if part.Offset < 0 || part.Length <= 0 || part.Offset > len(units) || part.Length > len(units)-part.Offset {
			continue
		}
		end := part.Offset + part.Length
		if !boundary(part.Offset) || !boundary(end) {
			continue
		}
		value := string(utf16.Decode(units[part.Offset:end]))
		switch part.Type {
		case "bot_command":
			command, target, addressed := strings.Cut(value, "@")
			if addressed {
				if !strings.EqualFold(target, self.Username) {
					return "", false, true
				}
				edits = append(edits, textEdit{part.Offset, end, utf16.Encode([]rune(command))})
			}
		case "mention", "text_mention":
			own := part.Type == "mention" && strings.EqualFold(value, "@"+self.Username) || part.Type == "text_mention" && part.User != nil && part.User.ID == self.ID
			if own {
				mentioned = true
				edits = append(edits, textEdit{start: part.Offset, end: end})
			}
		}
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	previous := len(units)
	for _, edit := range edits {
		if edit.end > previous {
			continue
		}
		units = append(append(units[:edit.start:edit.start], edit.value...), units[edit.end:]...)
		previous = edit.start
	}
	text = strings.TrimSpace(string(utf16.Decode(units)))
	// 宿主运维路由也会直接解析 Text；即使缺少实体，也不能把 OtherBot 命令交给它。
	if fields := strings.Fields(text); len(fields) > 0 && strings.HasPrefix(fields[0], "/") {
		command, target, addressed := strings.Cut(fields[0], "@")
		if addressed {
			if !strings.EqualFold(target, self.Username) {
				return "", false, true
			}
			text = command + strings.TrimPrefix(text, fields[0])
		}
	}
	return text, mentioned, false
}

func (a *Adapter) inbound(m message) (messaging.InboundMessage, bool, error) {
	binding, bound := a.cfg.Groups[m.Chat.ID]
	if !bound || !m.Chat.supported() || m.IsTopic || m.ThreadID != 0 {
		return messaging.InboundMessage{}, false, nil
	}
	if m.MigrateTo != 0 || m.MigrateFrom != 0 {
		return messaging.InboundMessage{}, false, operation.New(operation.Conflict, "Telegram group migrated; update its binding")
	}
	if m.SenderChat != nil || m.From.ID <= 0 || m.From.IsBot {
		return messaging.InboundMessage{}, false, nil
	}
	// 本批只接文本；媒体与入群服务消息不降级为可能误导模型的普通文字。
	if len(m.NewMembers) > 0 {
		return messaging.InboundMessage{}, false, nil
	}
	if m.ID <= 0 || m.Date <= 0 {
		return messaging.InboundMessage{}, false, operation.New(operation.Invalid, "Telegram message id and date must be positive")
	}
	a.mu.Lock()
	self := a.self
	a.mu.Unlock()
	if m.From.ID == self.ID {
		return messaging.InboundMessage{}, false, nil
	}
	text, entities := m.Text, m.Entities
	if text == "" {
		text, entities = m.Caption, m.CaptionEntities
	}
	text, mentioned, ignore := normalizeText(text, entities, self)
	if ignore {
		return messaging.InboundMessage{}, false, nil
	}
	if m.hasMedia() {
		return messaging.InboundMessage{}, false, operation.New(operation.Unsupported, "Telegram inbound media is not implemented")
	}
	result := messaging.InboundMessage{ConversationID: binding.ConversationID, UserID: strconv.FormatInt(m.From.ID, 10), MessageID: strconv.FormatInt(m.ID, 10),
		UserName: strings.TrimSpace(m.From.FirstName + " " + m.From.LastName), Text: text, Mentioned: mentioned, Command: strings.HasPrefix(text, "/"), At: time.Unix(m.Date, 0),
		PromptSuffix: strings.TrimSpace(a.cfg.PromptSuffix + "\n" + binding.PromptSuffix)}
	if reply := m.Reply; reply != nil && reply.ID > 0 && reply.Chat.ID == m.Chat.ID && reply.Chat.supported() && !reply.IsTopic && reply.ThreadID == 0 {
		if reply.hasMedia() {
			return messaging.InboundMessage{}, false, operation.New(operation.Unsupported, "Telegram quoted media is not implemented")
		}
		result.ReplyToMessageID = strconv.FormatInt(reply.ID, 10)
		quoted := reply.Text
		if quoted == "" {
			quoted = reply.Caption
		}
		result.Quoted = &messaging.QuotedMessage{MessageID: result.ReplyToMessageID, Text: quoted}
		if reply.SenderChat == nil && reply.From.ID == self.ID {
			result.Mentioned = true
		}
	}
	return result, result.Text != "" || result.Mentioned || result.HasQuotedContent(), nil
}

func (a *Adapter) role(ctx context.Context, chatID, userID int64) string {
	a.rolesMu.Lock()
	defer a.rolesMu.Unlock()
	cached, exists := a.roles[chatID]
	if !exists || !cached.until.After(a.now()) {
		var admins []struct {
			Status string `json:"status"`
			User   user   `json:"user"`
		}
		err := a.call(ctx, "getChatAdministrators", map[string]any{"chat_id": chatID}, &admins, a.cfg.Timeout)
		cached = roleCache{until: a.now().Add(30 * time.Second), roles: map[int64]string{}}
		if err == nil {
			cached.until = a.now().Add(5 * time.Minute)
			for _, member := range admins {
				switch member.Status {
				case "creator":
					cached.roles[member.User.ID] = "owner"
				case "administrator":
					cached.roles[member.User.ID] = "admin"
				}
			}
		} else {
			a.logger.WarnContext(ctx, "telegram administrator lookup failed", "connector_id", a.cfg.ID, "failure", operation.Describe(err))
		}
		a.roles[chatID] = cached
	}
	if role := cached.roles[userID]; role != "" {
		return role
	}
	return "member"
}

func (a *Adapter) handle(ctx context.Context, handler platform.Handler, item update) error {
	if item.Membership != nil {
		a.rolesMu.Lock()
		delete(a.roles, item.Membership.Chat.ID)
		a.rolesMu.Unlock()
	}
	if item.Message == nil {
		return nil
	}
	message, ok, err := a.inbound(*item.Message)
	if err != nil || !ok {
		return err
	}
	message.PlatformRole = a.role(ctx, item.Message.Chat.ID, item.Message.From.ID)
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err = handler.HandleInbound(ctx, message)
	return err
}
