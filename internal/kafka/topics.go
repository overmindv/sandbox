package kafka

import (
	"errors"
	"strings"

	"overmindv/sandbox/internal/config"
)

const (
	HeaderTraceID       = "x-trace-id"
	HeaderSchemaVersion = "x-schema-version"
	HeaderProducer      = "x-producer"
)

type TopicNames struct {
	Requests   string
	Events     string
	Results    string
	DeadLetter string
}

func TopicNamesFromConfig(cfg config.KafkaTopics) TopicNames {
	return TopicNames{
		Requests:   cfg.Requests,
		Events:     cfg.Events,
		Results:    cfg.Results,
		DeadLetter: cfg.DeadLetter,
	}
}

func (t TopicNames) Validate() error {
	switch {
	case strings.TrimSpace(t.Requests) == "":
		return errors.New("requests topic is required")
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
