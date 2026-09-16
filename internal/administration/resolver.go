// Package administration resolves platform identities into the administrator
// flag consumed by operations and reflection components.
package administration

import (
	"strings"

	"github.com/soleapi/forgebot/internal/messaging"
)

type Identity struct {
	ConversationID string
	UserID         string
}

type Resolver struct {
	identities map[string]struct{}
}

func New(identities []Identity) Resolver {
	resolver := Resolver{identities: make(map[string]struct{}, len(identities))}
	for _, identity := range identities {
		resolver.identities[key(identity.ConversationID, identity.UserID)] = struct{}{}
	}
	return resolver
}

func (r Resolver) Mark(message messaging.InboundMessage) messaging.InboundMessage {
	_, message.Administrator = r.identities[key(message.ConversationID, message.UserID)]
	return message
}

func key(conversationID, userID string) string {
	return strings.TrimSpace(conversationID) + "\x00" + strings.TrimSpace(userID)
}
