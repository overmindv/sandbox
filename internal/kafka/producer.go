package kafka

import (
	"context"
	"strings"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"overmindv/sandbox/internal/config"
)

type Producer interface {
	WriteMessages(ctx context.Context, messages ...Message) error
	Close() error
}

type KafkaProducer struct {
	writer *kafkago.Writer
}

func NewProducer(cfg config.KafkaConfig) *KafkaProducer {
	return &KafkaProducer{
		writer: &kafkago.Writer{
			Addr:         kafkago.TCP(cfg.Brokers...),
			Balancer:     &kafkago.Hash{},
			RequiredAcks: requiredAcks(cfg.Producer.Acks),
			Compression:  compression(cfg.Producer.Compression),
			BatchTimeout: time.Duration(cfg.Producer.LingerMS) * time.Millisecond,
			ReadTimeout:  time.Duration(cfg.Producer.RequestTimeoutMS) * time.Millisecond,
			WriteTimeout: time.Duration(cfg.Producer.RequestTimeoutMS) * time.Millisecond,
		},
	}
}

func (p *KafkaProducer) WriteMessages(ctx context.Context, messages ...Message) error {
	kafkaMessages := make([]kafkago.Message, 0, len(messages))
	for _, msg := range messages {
		kafkaMessages = append(kafkaMessages, messageToKafka(msg))
	}
	return p.writer.WriteMessages(ctx, kafkaMessages...)
}

func (p *KafkaProducer) Close() error {
	return p.writer.Close()
}

func requiredAcks(value string) kafkago.RequiredAcks {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "none", "0":
		return kafkago.RequireNone
	case "one", "1":
		return kafkago.RequireOne
	default:
		return kafkago.RequireAll
	}
}

func compression(value string) kafkago.Compression {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "gzip":
		return kafkago.Gzip
	case "snappy":
		return kafkago.Snappy
	case "lz4":
		return kafkago.Lz4
	case "zstd":
		return kafkago.Zstd
	default:
		return 0
	}
}
