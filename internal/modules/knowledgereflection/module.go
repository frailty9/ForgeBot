// Package knowledgereflection observes bounded administrator answers and
// periodically promotes reusable knowledge into managed Markdown documents.
package knowledgereflection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/soleapi/forgebot/internal/agent"
	"github.com/soleapi/forgebot/internal/capability"
	"github.com/soleapi/forgebot/internal/conversation"
	"github.com/soleapi/forgebot/internal/history"
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/knowledge"
	"github.com/soleapi/forgebot/internal/modules"
	reflectioncontract "github.com/soleapi/forgebot/internal/reflection"
	reflectionpipeline "github.com/soleapi/forgebot/internal/reflection/pipeline"
	reflectionreflector "github.com/soleapi/forgebot/internal/reflection/reflector"
	searchcontract "github.com/soleapi/forgebot/internal/search"
)

const ID = "native.knowledge_reflection"

type Options struct {
	ConversationIDs            []string
	ScanInterval               time.Duration
	MessageTTL                 time.Duration
	QuietWindow                time.Duration
	MaxSessionDuration         time.Duration
	MaxMessagesPerConversation int
	MaxMessagesTotal           int
	MaxMessageChars            int
	MaxPairsPerScan            int
	MinConfidence              float64
	ExtraDirectory             string
}

type Factory struct{}

func (Factory) Descriptor() modules.Descriptor {
	return modules.Descriptor{
		ID: ID, DefaultEnabled: false,
		Provides:      []capability.ID{reflectioncontract.ControlKey.ID()},
		Requires:      []capability.ID{agent.CompleterKey.ID(), searchcontract.ControlKey.ID(), knowledge.PublisherFactoryKey.ID(), conversation.CatalogKey.ID(), history.ReaderKey.ID()},
		Registrations: []modules.RegistrationKind{modules.RegistrationHistory, modules.RegistrationJobs, modules.RegistrationStatus},
	}
}

func defaults() Options {
	return Options{ScanInterval: 15 * time.Minute, MessageTTL: 24 * time.Hour, QuietWindow: 3 * time.Minute, MaxSessionDuration: 30 * time.Minute, MaxMessagesPerConversation: 1000, MaxMessagesTotal: 10000, MaxMessageChars: 4000, MaxPairsPerScan: 50, MinConfidence: .9, ExtraDirectory: "extra"}
}

type rawOptions struct {
	ConversationIDs            *[]string `json:"conversation_ids"`
	ScanInterval               *string   `json:"scan_interval"`
	MessageTTL                 *string   `json:"message_ttl"`
	QuietWindow                *string   `json:"quiet_window"`
	MaxSessionDuration         *string   `json:"max_session_duration"`
	MaxMessagesPerConversation *int      `json:"max_messages_per_conversation"`
	MaxMessagesTotal           *int      `json:"max_messages_total"`
	MaxMessageChars            *int      `json:"max_message_chars"`
	MaxPairsPerScan            *int      `json:"max_pairs_per_scan"`
	MinConfidence              *float64  `json:"min_confidence"`
	ExtraDirectory             *string   `json:"extra_directory"`
}

func parse(raw json.RawMessage) (Options, error) {
	result := defaults()
	if len(raw) == 0 {
		return result, nil
	}
	var value rawOptions
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return Options{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Options{}, errors.New("invalid options suffix")
	}
	if value.ConversationIDs != nil {
		result.ConversationIDs = append([]string(nil), (*value.ConversationIDs)...)
	}
	if value.MaxMessagesPerConversation != nil {
		result.MaxMessagesPerConversation = *value.MaxMessagesPerConversation
	}
	if value.MaxMessagesTotal != nil {
		result.MaxMessagesTotal = *value.MaxMessagesTotal
	}
	if value.MaxMessageChars != nil {
		result.MaxMessageChars = *value.MaxMessageChars
	}
	if value.MaxPairsPerScan != nil {
		result.MaxPairsPerScan = *value.MaxPairsPerScan
	}
	if value.MinConfidence != nil {
		result.MinConfidence = *value.MinConfidence
	}
	if value.ExtraDirectory != nil {
		result.ExtraDirectory = strings.TrimSpace(*value.ExtraDirectory)
	}
	var err error
	for rawValue, target := range map[*string]*time.Duration{value.ScanInterval: &result.ScanInterval, value.MessageTTL: &result.MessageTTL, value.QuietWindow: &result.QuietWindow, value.MaxSessionDuration: &result.MaxSessionDuration} {
		if rawValue != nil {
			*target, err = time.ParseDuration(*rawValue)
			if err != nil {
				return Options{}, errors.New("reflection duration is invalid")
			}
		}
	}
	if err := validate(result, false, nil); err != nil {
		return Options{}, err
	}
	return result, nil
}

func validate(options Options, requireConversations bool, catalog conversation.Catalog) error {
	if requireConversations && len(options.ConversationIDs) == 0 {
		return errors.New("conversation_ids must not be empty")
	}
	seen := make(map[string]bool, len(options.ConversationIDs))
	for _, id := range options.ConversationIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return errors.New("conversation_ids contain an empty or duplicate id")
		}
		if catalog != nil && !catalog.Has(id) {
			return errors.New("conversation_id must reference a bound conversation")
		}
		seen[id] = true
	}
	if options.ScanInterval <= 0 || options.MessageTTL <= 0 || options.QuietWindow <= 0 || options.MaxSessionDuration <= 0 || options.QuietWindow > options.MaxSessionDuration {
		return errors.New("reflection durations are invalid")
	}
	if options.MaxMessagesPerConversation <= 0 || options.MaxMessagesTotal <= 0 || options.MaxMessageChars <= 0 || options.MaxPairsPerScan <= 0 {
		return errors.New("reflection limits must be positive")
	}
	if options.MinConfidence <= 0 || options.MinConfidence > 1 {
		return errors.New("min_confidence must be greater than 0 and at most 1")
	}
	if strings.TrimSpace(options.ExtraDirectory) == "" {
		return errors.New("extra_directory is required")
	}
	return nil
}

func (Factory) ValidateOptions(raw json.RawMessage) error { _, err := parse(raw); return err }

// ReferencedGroups 供宿主核对沉淀所监听的群均已绑定。
func (Factory) ReferencedGroups(raw json.RawMessage) ([]string, error) {
	options, err := parse(raw)
	if err != nil {
		return nil, err
	}
	return append([]string(nil), options.ConversationIDs...), nil
}
func (Factory) PublicOptions(raw json.RawMessage) (any, error) {
	options, err := parse(raw)
	if err != nil {
		return nil, err
	}
	return map[string]any{"conversation_ids": options.ConversationIDs, "scan_interval": options.ScanInterval.String(), "message_ttl": options.MessageTTL.String(), "quiet_window": options.QuietWindow.String(), "max_session_duration": options.MaxSessionDuration.String(), "max_messages_per_conversation": options.MaxMessagesPerConversation, "max_messages_total": options.MaxMessagesTotal, "max_message_chars": options.MaxMessageChars, "max_pairs_per_scan": options.MaxPairsPerScan, "min_confidence": options.MinConfidence, "extra_directory": options.ExtraDirectory}, nil
}
func (Factory) Build(context.Context, json.RawMessage) (modules.Instance, error) {
	return nil, errors.New("knowledge reflection requires build context")
}
func (Factory) BuildWithContext(_ context.Context, build modules.BuildContext, raw json.RawMessage) (modules.Instance, error) {
	options, err := parse(raw)
	if err != nil {
		return nil, err
	}
	completer, ok := capability.Resolve(build.Capabilities, agent.CompleterKey)
	if !ok || completer == nil {
		return nil, errors.New("agent completer capability is unavailable")
	}
	search, ok := capability.Resolve(build.Capabilities, searchcontract.ControlKey)
	if !ok || search == nil {
		return nil, errors.New("search control capability is unavailable")
	}
	publishers, ok := capability.Resolve(build.Capabilities, knowledge.PublisherFactoryKey)
	if !ok || publishers == nil {
		return nil, errors.New("publisher factory capability is unavailable")
	}
	catalog, ok := capability.Resolve(build.Capabilities, conversation.CatalogKey)
	if !ok || catalog == nil {
		return nil, errors.New("conversation catalog capability is unavailable")
	}
	if err := validate(options, true, catalog); err != nil {
		return nil, err
	}
	reader, ok := capability.Resolve(build.Capabilities, history.ReaderKey)
	if !ok || reader == nil {
		return nil, errors.New("message history reader capability is unavailable")
	}
	reflector, err := reflectionreflector.New(completer, completer.Model())
	if err != nil {
		return nil, err
	}
	publisher, err := publishers.New(options.ExtraDirectory)
	if err != nil {
		return nil, err
	}
	processor, err := reflectionpipeline.New(reflectionpipeline.Config{Reflector: reflector, Publisher: publisher, MinConfidence: options.MinConfidence, Reload: func(ctx context.Context) error { _, reloadErr := search.Reload(ctx); return reloadErr }})
	if err != nil {
		return nil, err
	}
	return newInstance(options, reader, processor), nil
}

func (i *instance) Registrations() hooks.Registrations { return hooks.Registrations{} }
func (i *instance) ExtendedRegistrations() modules.ExtendedRegistrations {
	return modules.ExtendedRegistrations{
		History:      []history.Subscriber{{ID: "answer_sessions", Appended: i.observe}},
		Jobs:         []modules.BackgroundJob{{ID: "periodic_scan", Run: i.run}},
		Capabilities: []modules.CapabilityRegistration{{ID: reflectioncontract.ControlKey.ID(), Value: reflectioncontract.Control(i)}},
		Status:       func() any { return i.Status() },
	}
}
