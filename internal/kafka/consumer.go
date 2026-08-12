package kafka

import (
	"context"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"overmindv/sandbox/internal/config"
)

type Message struct {
	Topic     string
	Partition int
	Offset    int64
	Key       []byte
	Value     []byte
	Headers   map[string]string
	Time      time.Time
}

type Consumer interface {
	FetchMessage(ctx context.Context) (Message, error)
	CommitMessages(ctx context.Context, messages ...Message) error
	Close() error
}

type KafkaConsumer struct {
	reader *kafkago.Reader
}

func NewConsumer(cfg config.KafkaConfig) (*KafkaConsumer, error) {
	startOffset := kafkago.FirstOffset
	if cfg.Consumer.AutoOffsetReset == "latest" {
		startOffset = kafkago.LastOffset
	}

	queueCapacity := cfg.Consumer.MaxPollRecords
	if queueCapacity <= 0 {
		queueCapacity = 16
	}

	readerConfig := kafkago.ReaderConfig{
		Brokers:           cfg.Brokers,
		GroupID:           cfg.GroupID,
		Topic:             cfg.Topics.Requests,
		QueueCapacity:     queueCapacity,
		MinBytes:          1,
		MaxBytes:          10 * 1024 * 1024,
		MaxWait:           1 * time.Second,
		SessionTimeout:    time.Duration(cfg.Consumer.SessionTimeoutMS) * time.Millisecond,
		HeartbeatInterval: time.Duration(cfg.Consumer.HeartbeatIntervalMS) * time.Millisecond,
		CommitInterval:    0,
		StartOffset:       startOffset,
	}
	if err := readerConfig.Validate(); err != nil {
		return nil, err
	}

	return &KafkaConsumer{reader: kafkago.NewReader(readerConfig)}, nil
}

func (c *KafkaConsumer) FetchMessage(ctx context.Context) (Message, error) {
	msg, err := c.reader.FetchMessage(ctx)
	if err != nil {
		return Message{}, err
	}
	return messageFromKafka(msg), nil
}

func (c *KafkaConsumer) CommitMessages(ctx context.Context, messages ...Message) error {
	kafkaMessages := make([]kafkago.Message, 0, len(messages))
	for _, msg := range messages {
		kafkaMessages = append(kafkaMessages, messageToKafka(msg))
	}
	return c.reader.CommitMessages(ctx, kafkaMessages...)
}

func (c *KafkaConsumer) Close() error {
	return c.reader.Close()
}

func messageFromKafka(msg kafkago.Message) Message {
	headers := make(map[string]string, len(msg.Headers))
	for _, header := range msg.Headers {
		headers[header.Key] = string(header.Value)
	}

	return Message{
		Topic:     msg.Topic,
		Partition: msg.Partition,
		Offset:    msg.Offset,
		Key:       cloneBytes(msg.Key),
		Value:     cloneBytes(msg.Value),
		Headers:   headers,
		Time:      msg.Time,
	}
}

func messageToKafka(msg Message) kafkago.Message {
	headers := make([]kafkago.Header, 0, len(msg.Headers))
	for key, value := range msg.Headers {
		headers = append(headers, kafkago.Header{Key: key, Value: []byte(value)})
	}

	return kafkago.Message{
		Topic:     msg.Topic,
		Partition: msg.Partition,
		Offset:    msg.Offset,
		Key:       cloneBytes(msg.Key),
		Value:     cloneBytes(msg.Value),
		Headers:   headers,
		Time:      msg.Time,
	}
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}
