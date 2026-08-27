package kafka

import (
	"errors"
	"strings"

	"overmindv/sandbox/internal/config"
)

const (
	HeaderTraceID           = "x-trace-id"
	HeaderSchemaVersion     = "x-schema-version"
	HeaderProducer          = "x-producer"
	HeaderRetryCount        = "x-retry-count"
	HeaderRetryReason       = "x-retry-reason"
	HeaderRetryClass        = "x-retry-error-class"
	HeaderRetryNotBefore    = "x-retry-not-before"
	HeaderOriginalTopic     = "x-original-topic"
	HeaderOriginalPartition = "x-original-partition"
	HeaderOriginalOffset    = "x-original-offset"
)

type TopicNames struct {
	Requests   string
	Retry      string
	Events     string
	Results    string
	DeadLetter string
}

func TopicNamesFromConfig(cfg config.KafkaTopics) TopicNames {
	return TopicNames{
		Requests:   cfg.Requests,
		Retry:      cfg.Retry,
		Events:     cfg.Events,
		Results:    cfg.Results,
		DeadLetter: cfg.DeadLetter,
	}
}

func (t TopicNames) Validate() error {
	switch {
	case strings.TrimSpace(t.Requests) == "":
		return errors.New("requests topic is required")
	case strings.TrimSpace(t.Retry) == "":
		return errors.New("retry topic is required")
	case strings.TrimSpace(t.Events) == "":
		return errors.New("events topic is required")
	case strings.TrimSpace(t.Results) == "":
		return errors.New("results topic is required")
	case strings.TrimSpace(t.DeadLetter) == "":
		return errors.New("deadletter topic is required")
	default:
		return nil
	}
}
