// Package knowledge contains the storage-neutral contract for accepted
// reflection results.
package knowledge

import (
	"github.com/soleapi/forgebot/internal/capability"
	"time"
)

// Entry is the storage-neutral representation of an accepted reflection.
// Source fields are identifiers only; message bodies and user identities are
// deliberately excluded from the persisted contract.
type Entry struct {
	Title                   string
	Summary                 string
	Body                    string // Ordinary Markdown, without the document title or summary.
	Tags                    []string
	ConversationID          string
	SourceQuestionMessageID string
	SourceAnswerMessageID   string
	CreatedAt               time.Time
}

type PublishStatus string

const (
	Published PublishStatus = "published"
	Duplicate PublishStatus = "duplicate"
)

type PublishResult struct {
	Status      PublishStatus
	Path        string
	ContentHash string
}

type Publisher interface {
	Publish(Entry) (PublishResult, error)
}

type PublisherFactory interface {
	New(extraDirectory string) (Publisher, error)
}

var PublisherFactoryKey = capability.NewKey[PublisherFactory]("knowledge.publisher_factory.v1")
