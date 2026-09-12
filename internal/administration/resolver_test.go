package administration

import (
	"encoding/json"
	"testing"

	"github.com/soleapi/forgebot/internal/messaging"
)

func TestResolverScopesAdministratorToConversation(t *testing.T) {
	resolver := New([]Identity{{ConversationID: "support", UserID: "42"}})
	if message := resolver.Mark(messaging.InboundMessage{ConversationID: "support", UserID: "42"}); !message.Administrator {
		t.Fatal("configured administrator was not marked")
	}
	if message := resolver.Mark(messaging.InboundMessage{ConversationID: "other", UserID: "42"}); message.Administrator {
		t.Fatal("administrator identity leaked across conversations")
	}
}

func TestAdministratorFlagCannotBeSetThroughJSON(t *testing.T) {
	var message messaging.InboundMessage
	if err := json.Unmarshal([]byte(`{"conversation_id":"support","user_id":"42","administrator":true}`), &message); err != nil {
		t.Fatal(err)
	}
	if message.Administrator {
		t.Fatal("administrator flag was accepted from JSON")
	}
}
