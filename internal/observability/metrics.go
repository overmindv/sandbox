package observability

import "sync"

type SchedulerMetrics struct {
	mu                 sync.RWMutex
	queueDepth         int
	queueCapacity      int
	inFlight           int
	inFlightByLanguage map[string]int
	submitted          map[string]int64
	rejected           map[string]int64
	started            map[string]int64
	completed          map[string]int64
	timedOut           map[string]int64
}

type SchedulerMetricsSnapshot struct {
	QueueDepth         int
	QueueCapacity      int
	InFlight           int
	InFlightByLanguage map[string]int
	Submitted          map[string]int64
	Rejected           map[string]int64
	Started            map[string]int64
	Completed          map[string]int64
	TimedOut           map[string]int64
}

func NewSchedulerMetrics() *SchedulerMetrics {
	return &SchedulerMetrics{
		inFlightByLanguage: make(map[string]int),
		submitted:          make(map[string]int64),
		rejected:           make(map[string]int64),
		started:            make(map[string]int64),
		completed:          make(map[string]int64),
		timedOut:           make(map[string]int64),
	}
}

func (m *SchedulerMetrics) ObserveQueue(depth, capacity int) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queueDepth = depth
	m.queueCapacity = capacity
}

func (m *SchedulerMetrics) ObserveSlots(inFlight int, byLanguage map[string]int) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inFlight = inFlight
	m.inFlightByLanguage = cloneIntMap(byLanguage)
}

func (m *SchedulerMetrics) IncSubmitted(language string) {
	if m == nil {
		return
	}
	m.inc(m.submitted, language)
}

func (m *SchedulerMetrics) IncRejected(reason string) {
	if m == nil {
		return
	}
	m.inc(m.rejected, reason)
}

func (m *SchedulerMetrics) IncStarted(language string) {
	if m == nil {
		return
	}
	m.inc(m.started, language)
}

func (m *SchedulerMetrics) IncCompleted(status string) {
	if m == nil {
		return
	}
	m.inc(m.completed, status)
}

func (m *SchedulerMetrics) IncTimedOut(language string) {
	if m == nil {
		return
	}
	m.inc(m.timedOut, language)
}

func (m *SchedulerMetrics) Snapshot() SchedulerMetricsSnapshot {
	if m == nil {
		return SchedulerMetricsSnapshot{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return SchedulerMetricsSnapshot{
		QueueDepth:         m.queueDepth,
		QueueCapacity:      m.queueCapacity,
		InFlight:           m.inFlight,
		InFlightByLanguage: cloneIntMap(m.inFlightByLanguage),
		Submitted:          cloneInt64Map(m.submitted),
		Rejected:           cloneInt64Map(m.rejected),
		Started:            cloneInt64Map(m.started),
		Completed:          cloneInt64Map(m.completed),
		TimedOut:           cloneInt64Map(m.timedOut),
	}
}

func (m *SchedulerMetrics) inc(values map[string]int64, key string) {
	if m == nil {
		return
	}
	if key == "" {
		key = "unknown"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	values[key]++
}

func cloneIntMap(source map[string]int) map[string]int {
	target := make(map[string]int, len(source))
	for key, value := range source {
		target[key] = value
	}
	return target
}

func cloneInt64Map(source map[string]int64) map[string]int64 {
	target := make(map[string]int64, len(source))
	for key, value := range source {
		target[key] = value
	}
	return target
}
