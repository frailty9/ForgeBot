package knowledge

import "time"

// Entry is the storage-neutral representation of an accepted reflection.
// Source fields are identifiers only; message bodies and user identities are
// deliberately excluded from the persisted contract.
type Entry struct {
	Title                   string
	Question                string
	Answer                  string
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
