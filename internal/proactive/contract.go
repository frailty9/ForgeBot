// Package proactive defines the protocol-neutral control contract for delayed
// participation in selected group conversations.
package proactive

import (
	"github.com/soleapi/forgebot/internal/capability"
	"github.com/soleapi/forgebot/internal/gate"
)

var ControlKey = capability.NewKey[Control]("proactive.control.v1")

type Control interface {
	Stats() gate.Stats
}
