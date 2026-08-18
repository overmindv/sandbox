package app

import (
	"context"
	"os"
	"sort"
	"time"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/storage"
)

func registerWorkerInventory(ctx context.Context, store storage.RunStore, cfg config.Config) error {
	if store == nil {
		return nil
	}

	now := time.Now().UTC()
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = cfg.App.WorkerID
	}

	if err := store.UpsertWorkerInstance(ctx, storage.WorkerInstanceRecord{
		WorkerInstanceID: cfg.App.WorkerID,
		Hostname:         hostname,
		Environment:      cfg.App.Environment,
		ExecutorType:     cfg.Executor.Type,
		Status:           storage.WorkerStatusActive,
		StartedAt:        now,
		LastSeenAt:       now,
		CreatedAt:        now,
		UpdatedAt:        now,
	}); err != nil {
		return err
	}

	capabilities := make([]storage.WorkerCapabilityRecord, 0, len(cfg.Scheduler.MaxInFlightPerLanguage))
	languages := make([]string, 0, len(cfg.Scheduler.MaxInFlightPerLanguage))
	for languageID := range cfg.Scheduler.MaxInFlightPerLanguage {
		languages = append(languages, languageID)
	}
	sort.Strings(languages)

	for _, languageID := range languages {
		maxConcurrency := cfg.Scheduler.MaxInFlightPerLanguage[languageID]
		if maxConcurrency <= 0 {
			continue
		}
		capabilities = append(capabilities, storage.WorkerCapabilityRecord{
			WorkerInstanceID: cfg.App.WorkerID,
			LanguageID:       languageID,
			LanguageVersion:  "*",
			MaxConcurrency:   maxConcurrency,
			CreatedAt:        now,
			UpdatedAt:        now,
		})
	}
	return store.ReplaceWorkerCapabilities(ctx, cfg.App.WorkerID, capabilities)
}
