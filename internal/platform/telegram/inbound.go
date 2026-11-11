package telegram

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/soleapi/forgebot/internal/group"
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
	ID              int64       `json:"message_id"`
	Date            int64       `json:"date"`
	Chat            chat        `json:"chat"`
	From            user        `json:"from"`
	SenderChat      *chat       `json:"sender_chat"`
	IsTopic         bool        `json:"is_topic_message"`
	Text            string      `json:"text"`
	Caption         string      `json:"caption"`
	Entities        []entity    `json:"entities"`
	CaptionEntities []entity    `json:"caption_entities"`
	Reply           *message    `json:"reply_to_message"`
	NewMembers      []user      `json:"new_chat_members"`
	MigrateTo       int64       `json:"migrate_to_chat_id"`
	MigrateFrom     int64       `json:"migrate_from_chat_id"`
	Photo           []photoSize `json:"photo"`
	Document        *fileRef    `json:"document"`
	Video           *fileRef    `json:"video"`
	Audio           *fileRef    `json:"audio"`
	Voice           *fileRef    `json:"voice"`
	Animation       *fileRef    `json:"animation"`
	Sticker         *fileRef    `json:"sticker"`
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

// inbound 只做映射，不联网；图片以 file_id 形式交给工作者下载。
type inboundMessage struct {
	messaging.InboundMessage
	images, quotedImages []pendingImage
}

func (a *Adapter) inbound(m message) (inboundMessage, bool, error) {
	binding, bound := a.cfg.Groups[m.Chat.ID]
	// 非论坛超级群里，回复消息也带 message_thread_id（回复链根 ID），只有 is_topic_message 表示论坛话题。
	if !bound || !m.Chat.supported() || m.IsTopic {
		return inboundMessage{}, false, nil
	}
	if m.MigrateTo != 0 || m.MigrateFrom != 0 {
		return inboundMessage{}, false, operation.New(operation.Conflict, "Telegram group migrated; update its binding")
	}
	if m.SenderChat != nil || m.From.ID <= 0 || m.From.IsBot {
		return inboundMessage{}, false, nil
	}
	// 入群服务消息由 joinEvents 转为成员事件，不作为空文本问答。
	if len(m.NewMembers) > 0 {
		return inboundMessage{}, false, nil
	}
	if m.ID <= 0 || m.Date <= 0 {
		return inboundMessage{}, false, operation.New(operation.Invalid, "Telegram message id and date must be positive")
	}
	a.mu.Lock()
	self := a.self
	a.mu.Unlock()
	if m.From.ID == self.ID {
		return inboundMessage{}, false, nil
	}
	text, entities := m.Text, m.Entities
	if text == "" {
		text, entities = m.Caption, m.CaptionEntities
	}
	text, mentioned, ignore := normalizeText(text, entities, self)
	if ignore {
		return inboundMessage{}, false, nil
	}
	images, others, err := mediaOf(&m)
	if err != nil {
		return inboundMessage{}, false, err
	}
	result := inboundMessage{InboundMessage: messaging.InboundMessage{ConversationID: binding.ConversationID, UserID: strconv.FormatInt(m.From.ID, 10), MessageID: strconv.FormatInt(m.ID, 10),
		UserName: strings.TrimSpace(m.From.FirstName + " " + m.From.LastName), Text: text, Media: others, Mentioned: mentioned, Command: strings.HasPrefix(text, "/"), At: time.Unix(m.Date, 0),
		PromptSuffix: strings.TrimSpace(a.cfg.PromptSuffix + "\n" + binding.PromptSuffix)}, images: images}
	if reply := m.Reply; reply != nil && reply.ID > 0 && reply.Chat.ID == m.Chat.ID && reply.Chat.supported() && !reply.IsTopic {
		quotedImages, quotedOthers, err := mediaOf(reply)
		if err != nil {
			return inboundMessage{}, false, err
		}
		result.ReplyToMessageID = strconv.FormatInt(reply.ID, 10)
		quoted := reply.Text
		if quoted == "" {
			quoted = reply.Caption
		}
		fromSelf := reply.SenderChat == nil && reply.From.ID == self.ID
		result.Quoted = &messaging.QuotedMessage{MessageID: result.ReplyToMessageID, Text: quoted, Media: quotedOthers, FromSelf: fromSelf}
		result.quotedImages = quotedImages
		if fromSelf {
			result.Mentioned = true
		}
	}
	quotedContent := result.Quoted != nil && (result.Quoted.Text != "" || len(result.Quoted.Media) > 0 || len(result.quotedImages) > 0)
	return result, result.Text != "" || result.Mentioned || len(result.Media) > 0 || len(result.images) > 0 || quotedContent, nil
}

// memberJoined 为服务消息里的每位真人生成一个 MemberJoined。身份取 new_chat_members 元素，
// 不取 from（可能是邀请人）；时间取服务消息的 date，缺失或非法时不伪造当前时间。
// 平台重投由 update_id 去重；事件 ID 含 update_id，真人短期内重新入群仍是新事件。
func (a *Adapter) memberJoined(ctx context.Context, handler platform.Handler, item update) error {
	m := item.Message
	binding, bound := a.cfg.Groups[m.Chat.ID]
	if !bound || !m.Chat.supported() || m.IsTopic {
		return nil
	}
	consumer, ok := handler.(group.EventHandler)
	if !ok {
		return nil
	}
	if m.Date <= 0 {
		return operation.New(operation.Invalid, "Telegram join service message has no valid date")
	}
	a.mu.Lock()
	self := a.self
	a.mu.Unlock()
	chatID, updateID := strconv.FormatInt(m.Chat.ID, 10), strconv.FormatInt(item.ID, 10)
	var failures []error
	for _, member := range m.NewMembers {
		if member.ID <= 0 || member.IsBot || member.ID == self.ID {
			continue
		}
		userID := strconv.FormatInt(member.ID, 10)
		event := group.Event{ID: group.ID(a.cfg.ID, chatID, updateID, userID), Kind: group.MemberJoined, ConversationID: binding.ConversationID,
			ConnectorID: a.cfg.ID, UserID: userID, UserName: strings.TrimSpace(member.FirstName + " " + member.LastName), At: time.Unix(m.Date, 0)}
		if _, err := consumer.HandleGroupEvent(ctx, event); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (a *Adapter) role(ctx context.Context, chatID, userID int64) string {
	a.rolesMu.Lock()
	cached, exists := a.roles[chatID]
	a.rolesMu.Unlock()
	// 网络查询不持锁，避免一个群的慢请求阻塞所有工作者；并发未命中最多多查几次。
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
		a.rolesMu.Lock()
		a.roles[chatID] = cached
		a.rolesMu.Unlock()
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
	// 入群服务消息的 from 可能是邀请人（甚至是机器人），必须先于发送者过滤处理。
	if len(item.Message.NewMembers) > 0 {
		return a.memberJoined(ctx, handler, item)
	}
	mapped, ok, err := a.inbound(*item.Message)
	if err != nil || !ok {
		return err
	}
	message := mapped.InboundMessage
	// 下载在工作者中进行；当前与引用图片共享单条消息的总量上限，任一失败整条停止，不丢图后问答。
	if pending := append(append([]pendingImage(nil), mapped.images...), mapped.quotedImages...); len(pending) > 0 {
		downloaded, err := a.downloadImages(ctx, pending)
		if err != nil {
			return err
		}
		message.Media = append(downloaded[:len(mapped.images):len(mapped.images)], message.Media...)
		if message.Quoted != nil && len(mapped.quotedImages) > 0 {
			quoted := *message.Quoted
			quoted.Media = append(append([]messaging.Media(nil), downloaded[len(mapped.images):]...), quoted.Media...)
			message.Quoted = &quoted
		}
	}
	message.PlatformRole = a.role(ctx, item.Message.Chat.ID, item.Message.From.ID)
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err = handler.HandleInbound(ctx, message)
	return err
}
