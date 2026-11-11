package telegram

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/soleapi/forgebot/internal/operation"
)

var allowedUpdates = []string{"message", "my_chat_member"}

func (a *Adapter) getUpdates(ctx context.Context, seconds int) ([]update, error) {
	a.mu.Lock()
	offset := a.offset
	a.mu.Unlock()
	var updates []update
	err := a.call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": seconds, "limit": 20, "allowed_updates": allowedUpdates}, &updates, time.Duration(seconds)*time.Second+a.cfg.Timeout)
	if err != nil {
		return nil, err
	}
	if len(updates) > 20 {
		return nil, operation.New(operation.Invalid, "Telegram update batch exceeds requested limit")
	}
	for _, item := range updates {
		if item.ID < 0 || item.ID == math.MaxInt64 {
			return nil, operation.New(operation.Invalid, "invalid Telegram update id")
		}
	}
	return updates, nil
}

func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// accept 在入队与记账间持锁；等待队列/记账空间时释放锁，Stop 始终能够取消。
func (a *Adapter) accept(ctx context.Context, r *receiver, item update) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		a.mu.Lock()
		now := a.now()
		for id, until := range a.replays {
			if !until.After(now) {
				delete(a.replays, id)
			}
		}
		_, seen := a.replays[item.ID]
		accepted := seen || item.Message == nil && item.Membership == nil
		if !accepted && len(a.replays) < 10000 {
			select {
			case r.queue <- item:
				a.replays[item.ID] = now.Add(5 * time.Minute)
				accepted = true
			default:
			}
		}
		if accepted {
			if next := item.ID + 1; next > a.offset {
				a.offset = next
			}
		}
		a.mu.Unlock()
		if accepted {
			return nil
		}
		if err := wait(ctx, 10*time.Millisecond); err != nil {
			return err
		}
	}
}

func retryDelay(err error, backoff time.Duration) time.Duration {
	var remote *apiFailure
	if errors.As(err, &remote) && remote.parameters.RetryAfter > 0 {
		seconds := remote.parameters.RetryAfter
		if seconds > math.MaxInt64/int64(time.Second) {
			return time.Duration(math.MaxInt64)
		}
		return time.Duration(seconds) * time.Second
	}
	return backoff
}

func (a *Adapter) poll(r *receiver) {
	backoff := time.Second
	for r.ctx.Err() == nil {
		updates, err := a.getUpdates(r.ctx, 30)
		if r.ctx.Err() != nil {
			return
		}
		if err != nil {
			code := operation.Describe(err).Code
			if code == operation.Unauthenticated || code == operation.PermissionDenied || code == operation.Conflict {
				select {
				case a.errors <- err:
				default:
				}
				r.cancel()
				return
			}
			a.logger.WarnContext(r.ctx, "telegram polling failed", "connector_id", a.cfg.ID, "failure", operation.Describe(err))
			if wait(r.ctx, retryDelay(err, backoff)) != nil {
				return
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		backoff = time.Second
		for _, item := range updates {
			if a.accept(r.ctx, r, item) != nil {
				return
			}
		}
	}
}
