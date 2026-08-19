package observability

import (
	"sync"
	"time"
)

type SchedulerMetrics struct {
	mu                         sync.RWMutex
	queueDepth                 int
	queueCapacity              int
	inFlight                   int
	inFlightByLanguage         map[string]int
	submitted                  map[string]int64
	rejected                   map[string]int64
	started                    map[string]int64
	completed                  map[string]int64
	timedOut                   map[string]int64
	consumed                   map[string]int64
	produced                   map[string]int64
	messageFailures            map[string]int64
	resultStatuses             map[string]int64
	resultLanguages            map[string]int64
	processingDurationsByStage map[string]DurationStats
	queueLagByTopic            map[string]DurationStats
	executorLatencyByLanguage  map[string]DurationStats
	retriesScheduled           map[string]int64
	retriesDelayed             map[string]int64
	retriesExhausted           map[string]int64
	dlqPublished               map[string]int64
	errorResults               map[string]int64
	artifactsStored            map[string]int64
	artifactBytes              map[string]int64
	artifactFailures           map[string]int64
}

type DurationStats struct {
	Count int64
	SumMS int64
	MaxMS int64
}

type SchedulerMetricsSnapshot struct {
	QueueDepth                 int
	QueueCapacity              int
	InFlight                   int
	InFlightByLanguage         map[string]int
	Submitted                  map[string]int64
	Rejected                   map[string]int64
	Started                    map[string]int64
	Completed                  map[string]int64
	TimedOut                   map[string]int64
	Consumed                   map[string]int64
	Produced                   map[string]int64
	MessageFailures            map[string]int64
	ResultStatuses             map[string]int64
	ResultLanguages            map[string]int64
	ProcessingDurationsByStage map[string]DurationStats
	QueueLagByTopic            map[string]DurationStats
	ExecutorLatencyByLanguage  map[string]DurationStats
	RetriesScheduled           map[string]int64
	RetriesDelayed             map[string]int64
	RetriesExhausted           map[string]int64
	DLQPublished               map[string]int64
	ErrorResults               map[string]int64
	ArtifactsStored            map[string]int64
	ArtifactBytes              map[string]int64
	ArtifactFailures           map[string]int64
}

func NewSchedulerMetrics() *SchedulerMetrics {
	return &SchedulerMetrics{
		inFlightByLanguage:         make(map[string]int),
		submitted:                  make(map[string]int64),
		rejected:                   make(map[string]int64),
		started:                    make(map[string]int64),
		completed:                  make(map[string]int64),
		timedOut:                   make(map[string]int64),
		consumed:                   make(map[string]int64),
		produced:                   make(map[string]int64),
		messageFailures:            make(map[string]int64),
		resultStatuses:             make(map[string]int64),
		resultLanguages:            make(map[string]int64),
		processingDurationsByStage: make(map[string]DurationStats),
		queueLagByTopic:            make(map[string]DurationStats),
		executorLatencyByLanguage:  make(map[string]DurationStats),
		retriesScheduled:           make(map[string]int64),
		retriesDelayed:             make(map[string]int64),
		retriesExhausted:           make(map[string]int64),
		dlqPublished:               make(map[string]int64),
		errorResults:               make(map[string]int64),
		artifactsStored:            make(map[string]int64),
		artifactBytes:              make(map[string]int64),
		artifactFailures:           make(map[string]int64),
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

func (m *SchedulerMetrics) IncConsumed(topic string) {
	if m == nil {
		return
	}
	m.inc(m.consumed, topic)
}

func (m *SchedulerMetrics) IncProduced(topic string) {
	if m == nil {
		return
	}
	m.inc(m.produced, topic)
}

func (m *SchedulerMetrics) IncMessageFailure(reason string) {
	if m == nil {
		return
	}
	m.inc(m.messageFailures, reason)
}

func (m *SchedulerMetrics) IncResultStatus(status string) {
	if m == nil {
		return
	}
	m.inc(m.resultStatuses, status)
}

func (m *SchedulerMetrics) IncResultLanguage(language string) {
	if m == nil {
		return
	}
	m.inc(m.resultLanguages, language)
}

func (m *SchedulerMetrics) ObserveProcessingDuration(stage string, duration time.Duration) {
	if m == nil {
		return
	}
	m.observeDuration(m.processingDurationsByStage, stage, duration)
}

func (m *SchedulerMetrics) ObserveQueueLag(topic string, duration time.Duration) {
	if m == nil {
		return
	}
	m.observeDuration(m.queueLagByTopic, topic, duration)
}

func (m *SchedulerMetrics) ObserveExecutorLatency(language string, duration time.Duration) {
	if m == nil {
		return
	}
	m.observeDuration(m.executorLatencyByLanguage, language, duration)
}

func (m *SchedulerMetrics) IncRetryScheduled(reason string) {
	if m == nil {
		return
	}
	m.inc(m.retriesScheduled, reason)
}

func (m *SchedulerMetrics) IncRetryDelayed(reason string) {
	if m == nil {
		return
	}
	m.inc(m.retriesDelayed, reason)
}

func (m *SchedulerMetrics) IncRetryExhausted(reason string) {
	if m == nil {
		return
	}
	m.inc(m.retriesExhausted, reason)
}

func (m *SchedulerMetrics) IncDLQPublished(reason string) {
	if m == nil {
		return
	}
	m.inc(m.dlqPublished, reason)
}

func (m *SchedulerMetrics) IncErrorResult(reason string) {
	if m == nil {
		return
	}
	m.inc(m.errorResults, reason)
}

func (m *SchedulerMetrics) IncArtifactStored(kind string, bytes int64) {
	if m == nil {
		return
	}
	if bytes < 0 {
		bytes = 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if kind == "" {
		kind = "unknown"
	}
	m.artifactsStored[kind]++
	m.artifactBytes[kind] += bytes
}

func (m *SchedulerMetrics) IncArtifactFailure(reason string) {
	if m == nil {
		return
	}
	m.inc(m.artifactFailures, reason)
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
		Consumed:           cloneInt64Map(m.consumed),
		Produced:           cloneInt64Map(m.produced),
		MessageFailures:    cloneInt64Map(m.messageFailures),
		ResultStatuses:     cloneInt64Map(m.resultStatuses),
		ResultLanguages:    cloneInt64Map(m.resultLanguages),
		ProcessingDurationsByStage: cloneDurationStatsMap(
			m.processingDurationsByStage,
		),
		QueueLagByTopic: cloneDurationStatsMap(m.queueLagByTopic),
		ExecutorLatencyByLanguage: cloneDurationStatsMap(
			m.executorLatencyByLanguage,
		),
		RetriesScheduled: cloneInt64Map(m.retriesScheduled),
		RetriesDelayed:   cloneInt64Map(m.retriesDelayed),
		RetriesExhausted: cloneInt64Map(m.retriesExhausted),
		DLQPublished:     cloneInt64Map(m.dlqPublished),
		ErrorResults:     cloneInt64Map(m.errorResults),
		ArtifactsStored:  cloneInt64Map(m.artifactsStored),
		ArtifactBytes:    cloneInt64Map(m.artifactBytes),
		ArtifactFailures: cloneInt64Map(m.artifactFailures),
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

func (m *SchedulerMetrics) observeDuration(values map[string]DurationStats, key string, duration time.Duration) {
	if m == nil {
		return
	}
	if key == "" {
		key = "unknown"
	}
	if duration < 0 {
		duration = 0
	}

	ms := duration.Milliseconds()
	m.mu.Lock()
	defer m.mu.Unlock()
	stats := values[key]
	stats.Count++
	stats.SumMS += ms
	if ms > stats.MaxMS {
		stats.MaxMS = ms
	}
	values[key] = stats
}

func cloneIntMap(source map[string]int) map[string]int {
	target := make(map[string]int, len(source))
	for key, value := range source {
		target[key] = value
	}
	return target
}

func cloneDurationStatsMap(source map[string]DurationStats) map[string]DurationStats {
	target := make(map[string]DurationStats, len(source))
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
