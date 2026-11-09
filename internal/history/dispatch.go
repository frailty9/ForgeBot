package history

// enqueue 在持有 w.mu 时调用，因此事件进入队列的顺序就是写入顺序。
func (w *Window) enqueue(event pendingEvent) {
	w.dispatch.Lock()
	if len(w.queue) >= maxPendingEvents {
		w.queue = w.queue[1:]
		w.logger.Warn("message history dropped an undelivered event", "pending", maxPendingEvents)
	}
	w.queue = append(w.queue, event)
	w.dispatch.Unlock()
}

// drain 在锁外按顺序投递事件。同一时刻只有一个 goroutine 在投递：订阅者回调里如果
// 又写入了消息（例如触发了一次发送），新事件只会排队，由当前投递者接着送出，不会死锁。
func (w *Window) drain() {
	w.dispatch.Lock()
	if w.draining {
		w.dispatch.Unlock()
		return
	}
	w.draining = true
	for len(w.queue) > 0 {
		event := w.queue[0]
		w.queue = w.queue[1:]
		w.dispatch.Unlock()
		w.deliver(event)
		w.dispatch.Lock()
	}
	w.queue = nil
	w.draining = false
	w.dispatch.Unlock()
}

func (w *Window) deliver(event pendingEvent) {
	subscribers := w.subs.Load()
	if subscribers == nil {
		return
	}
	for _, subscriber := range *subscribers {
		if event.appended != nil && subscriber.Appended != nil {
			record := *event.appended
			w.call(subscriber.ID, func() { subscriber.Appended(record) })
		}
		if event.appended == nil && subscriber.Evicted != nil {
			records := append([]Record(nil), event.evicted...)
			w.call(subscriber.ID, func() { subscriber.Evicted(event.group, records, event.reason) })
		}
	}
}

func (w *Window) call(id string, fn func()) {
	defer func() {
		if recovered := recover(); recovered != nil {
			w.logger.Error("message history subscriber panicked", "subscriber", id, "panic", recovered)
		}
	}()
	fn()
}
