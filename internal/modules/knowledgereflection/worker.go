package knowledgereflection

import (
	"context"
	"time"

	"github.com/soleapi/forgebot/internal/operation"
	reflectioncontract "github.com/soleapi/forgebot/internal/reflection"
)

func (i *instance) run(ctx context.Context) error {
	i.mu.Lock()
	if i.retired {
		i.mu.Unlock()
		return nil
	}
	i.active = true
	i.mu.Unlock()
	defer func() { i.mu.Lock(); i.active = false; i.mu.Unlock() }()
	ticker := time.NewTicker(i.options.ScanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-i.lifetime.Done():
			return nil
		case <-ticker.C:
			_, _ = i.Scan(ctx)
		}
	}
}

func (i *instance) Scan(ctx context.Context) (reflectioncontract.ScanResult, error) {
	processed, retried, skipped, err := i.scanOnce(ctx)
	return reflectioncontract.ScanResult{Processed: processed, Retried: retried, Skipped: skipped, Status: i.Status()}, err
}

func (i *instance) scanOnce(ctx context.Context) (processed, retried int, skipped bool, err error) {
	if err := ctx.Err(); err != nil {
		return 0, 0, false, err
	}
	if !i.scanMu.TryLock() {
		i.mu.Lock()
		i.worker.Skipped++
		i.mu.Unlock()
		return 0, 0, true, nil
	}
	defer i.scanMu.Unlock()
	i.mu.Lock()
	if i.retired {
		i.mu.Unlock()
		return 0, 0, false, operation.New(operation.Unavailable, "reflection module is retired")
	}
	i.idle = make(chan struct{})
	i.worker.Scans++
	i.worker.Running = true
	i.worker.LastStarted = i.now().UTC()
	batch := i.batchLocked(i.worker.LastStarted)
	i.mu.Unlock()
	defer func() {
		i.mu.Lock()
		i.worker.Running = false
		i.worker.Processed += uint64(processed)
		i.worker.Retried += uint64(retried)
		i.worker.LastFinished = i.now().UTC()
		close(i.idle)
		i.mu.Unlock()
	}()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(i.lifetime, cancel)
	defer stop()
	defer cancel()
	errs := i.pipeline.ProcessBatch(ctx, batch)
	i.mu.Lock()
	defer i.mu.Unlock()
	for index, candidate := range batch {
		state := i.sessions[candidate.Key]
		if state != nil {
			state.processing = false
		}
		if errs[index] != nil {
			if state != nil {
				state.failures++
			}
			retried++
			i.worker.LastError = errs[index].Error()
			continue
		}
		delete(i.sessions, candidate.Key)
		if !i.retired {
			i.markLocked("session:"+candidate.Key, candidate.Question.ConversationID, i.now().UTC())
		}
		processed++
	}
	return processed, retried, false, ctx.Err()
}

func (i *instance) Status() reflectioncontract.Status {
	i.mu.Lock()
	i.expireLocked(i.now().UTC())
	pending := i.pending
	pending.Sessions, pending.Marks = len(i.sessions), len(i.marks)
	pending.Messages, _ = i.countsLocked()
	active, worker := i.active, i.worker
	i.mu.Unlock()
	return reflectioncontract.Status{Enabled: true, Active: active, Pending: pending, Worker: worker, Pipeline: i.pipeline.Stats()}
}

func (i *instance) Retire() {
	i.mu.Lock()
	i.retired = true
	clear(i.sessions)
	clear(i.marks)
	i.mu.Unlock()
	i.cancel()
}

func (i *instance) Close(ctx context.Context) error {
	i.Retire()
	i.mu.Lock()
	idle := i.idle
	i.mu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
