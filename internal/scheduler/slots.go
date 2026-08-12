package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrNoSlotsAvailable = errors.New("no scheduler slots available")
	ErrInvalidSlotLimit = errors.New("invalid scheduler slot limit")
)

type SlotPool struct {
	maxGlobal        int
	maxPerLanguage   map[string]int
	mu               sync.Mutex
	inFlight         int
	byLanguage       map[string]int
	acquirePollEvery time.Duration
}

type Slot struct {
	pool     *SlotPool
	language string
	once     sync.Once
}

type SlotStats struct {
	MaxGlobal      int
	InFlight       int
	MaxPerLanguage map[string]int
	ByLanguage     map[string]int
}

func NewSlotPool(maxGlobal int, maxPerLanguage map[string]int) (*SlotPool, error) {
	if maxGlobal <= 0 {
		return nil, fmt.Errorf("%w: max global must be positive", ErrInvalidSlotLimit)
	}
	cloned := make(map[string]int, len(maxPerLanguage))
	for language, limit := range maxPerLanguage {
		if language == "" {
			return nil, fmt.Errorf("%w: language must not be empty", ErrInvalidSlotLimit)
		}
		if limit <= 0 {
			return nil, fmt.Errorf("%w: %s must be positive", ErrInvalidSlotLimit, language)
		}
		cloned[language] = limit
	}

	return &SlotPool{
		maxGlobal:        maxGlobal,
		maxPerLanguage:   cloned,
		byLanguage:       make(map[string]int),
		acquirePollEvery: 5 * time.Millisecond,
	}, nil
}

func (p *SlotPool) Acquire(ctx context.Context, language string) (*Slot, error) {
	for {
		if slot, ok := p.TryAcquire(language); ok {
			return slot, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(p.acquirePollEvery):
		}
	}
}

func (p *SlotPool) TryAcquire(language string) (*Slot, bool) {
	if language == "" {
		language = "unknown"
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.inFlight >= p.maxGlobal {
		return nil, false
	}
	if maxLanguage, limited := p.maxPerLanguage[language]; limited && p.byLanguage[language] >= maxLanguage {
		return nil, false
	}

	p.inFlight++
	p.byLanguage[language]++
	return &Slot{pool: p, language: language}, true
}

func (s *Slot) Release() {
	if s == nil || s.pool == nil {
		return
	}
	s.once.Do(func() {
		s.pool.release(s.language)
	})
}

func (p *SlotPool) release(language string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.inFlight > 0 {
		p.inFlight--
	}
	if p.byLanguage[language] > 0 {
		p.byLanguage[language]--
	}
}

func (p *SlotPool) Stats() SlotStats {
	p.mu.Lock()
	defer p.mu.Unlock()

	return SlotStats{
		MaxGlobal:      p.maxGlobal,
		InFlight:       p.inFlight,
		MaxPerLanguage: cloneIntMap(p.maxPerLanguage),
		ByLanguage:     cloneIntMap(p.byLanguage),
	}
}

func cloneIntMap(source map[string]int) map[string]int {
	target := make(map[string]int, len(source))
	for key, value := range source {
		target[key] = value
	}
	return target
}
