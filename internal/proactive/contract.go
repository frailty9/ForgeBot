// Package proactive defines the protocol-neutral control contract for delayed
// participation in selected group conversations.
package proactive

import "github.com/soleapi/forgebot/internal/capability"

var ControlKey = capability.NewKey[Control]("proactive.control.v1")

type Control interface {
	Stats() Stats
}
