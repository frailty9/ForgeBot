package runtime

import (
	"time"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/operation"
)

func boundConversationIDs(cfg config.RuntimeConfig) []string {
	ids := make([]string, 0, len(cfg.Platforms.Groups))
	for _, group := range cfg.Platforms.Groups {
		ids = append(ids, group.ID)
	}
	return ids
}

// 控制入口只改变指定群的主动插话，管理员@和全局暂停走各自原有策略。
func (m *Manager) SetProactiveYield(group string, paused bool) error {
	snapshot, _ := m.Snapshot()
	found := false
	for _, binding := range snapshot.Runtime.Platforms.Groups {
		if binding.ID == group {
			found = true
			break
		}
	}
	if !found {
		return operation.New(operation.NotFound, "conversation is not bound")
	}
	if paused {
		m.builder.Yield.Suspend(group, 5*time.Minute)
	} else {
		m.builder.Yield.Resume(group)
	}
	return nil
}
