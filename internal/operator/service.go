package operator

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/operations"
	runtimecfg "github.com/soleapi/forgebot/internal/runtime"
	"github.com/soleapi/forgebot/internal/settings"
)

var ErrCapabilityUnavailable = errors.New("该能力尚未就绪")

type Service struct {
	runtime  Runtime
	settings settings.Store
}

type Runtime interface {
	Snapshot() (config.Snapshot, runtimecfg.Status)
	Restart(context.Context, string) error
	CommitRuntime(context.Context, config.RuntimeConfig, runtimecfg.Persist) (runtimecfg.ApplyResult, error)
	Paused() bool
	SetPaused(bool)
}

func New(runtime Runtime, store settings.Store) (*Service, error) {
	if runtime == nil {
		return nil, errors.New("runtime manager is required")
	}
	return &Service{runtime: runtime, settings: store}, nil
}

func (s *Service) Execute(_ context.Context, request operations.Request) (operations.Result, error) {
	switch request.Command {
	case operations.CommandStatus:
		if err := requireNoArguments(request); err != nil {
			return operations.Result{}, err
		}
		return operations.Result{Reply: s.status()}, nil
	case operations.CommandPause:
		if err := requireNoArguments(request); err != nil {
			return operations.Result{}, err
		}
		s.runtime.SetPaused(true)
		return operations.Result{Reply: "Bot 普通消息处理已暂停，管理命令仍可使用。"}, nil
	case operations.CommandResume:
		if err := requireNoArguments(request); err != nil {
			return operations.Result{}, err
		}
		s.runtime.SetPaused(false)
		return operations.Result{Reply: "Bot 普通消息处理已恢复。"}, nil
	case operations.CommandRestart:
		if len(request.Arguments) != 0 {
			return operations.Result{}, errors.New("用法：/restart")
		}
		return operations.Result{
			Reply: "Runtime 重启已受理。",
			Followup: func(ctx context.Context) (string, error) {
				if err := s.runtime.Restart(ctx, "operator:"+request.ConversationID); err != nil {
					return "", err
				}
				return "Runtime 重启完成。", nil
			},
		}, nil
	case operations.CommandGate:
		return s.gate(request)
	case operations.CommandReloadDocs:
		return operations.Result{}, ErrCapabilityUnavailable
	default:
		return operations.Result{}, operations.ErrUnsupportedCommand
	}
}

func (s *Service) status() string {
	snapshot, status := s.runtime.Snapshot()
	connectorStates := make([]string, 0, len(status.Connectors))
	for _, connector := range status.Connectors {
		connectorStates = append(connectorStates, connector.ID+"="+string(connector.State))
	}
	if len(connectorStates) == 0 {
		connectorStates = append(connectorStates, "无")
	}
	return fmt.Sprintf("Runtime: %s\n版本: %d\nBot: %s\nGate: %s\nConnector: %s",
		status.State, snapshot.Version, enabled(!s.runtime.Paused()), enabled(snapshot.Runtime.Gate.Enabled), strings.Join(connectorStates, ", "))
}

func (s *Service) gate(request operations.Request) (operations.Result, error) {
	snapshot, _ := s.runtime.Snapshot()
	if len(request.Arguments) == 0 {
		return operations.Result{Reply: "Gate 当前为 " + enabled(snapshot.Runtime.Gate.Enabled) + "。"}, nil
	}
	if len(request.Arguments) != 1 {
		return operations.Result{}, errors.New("用法：/gate on|off")
	}
	var enabledValue bool
	switch strings.ToLower(strings.TrimSpace(request.Arguments[0])) {
	case "on":
		enabledValue = true
	case "off":
		enabledValue = false
	default:
		return operations.Result{}, errors.New("用法：/gate on|off")
	}
	if s.settings == nil {
		return operations.Result{}, errors.New("运行时配置存储不可用")
	}
	if snapshot.Runtime.Gate.Enabled == enabledValue {
		return operations.Result{Reply: "Gate 已经是 " + enabled(enabledValue) + "。"}, nil
	}
	return operations.Result{
		Reply: "Gate 切换已受理。",
		Followup: func(ctx context.Context) (string, error) {
			current, _ := s.runtime.Snapshot()
			next := current.Runtime
			next.Gate.Enabled = enabledValue
			_, err := s.runtime.CommitRuntime(ctx, next, func() (config.Snapshot, error) {
				return s.settings.Update(ctx, settings.UpdateRequest{
					ExpectedVersion: current.Version, Runtime: next,
					Source: "operator:" + request.ConversationID,
				})
			})
			if err != nil {
				return "", err
			}
			return "Gate 已切换为 " + enabled(enabledValue) + "。", nil
		},
	}, nil
}

func enabled(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

func requireNoArguments(request operations.Request) error {
	if len(request.Arguments) != 0 {
		return fmt.Errorf("用法：/%s", request.Command)
	}
	return nil
}
