package kafka

import (
	"context"
	"testing"

	"overmindv/sandbox/internal/contracts"
)

func BenchmarkProcessOnce(b *testing.B) {
	req := validKafkaRunRequest()
	payload, err := EncodeRunRequest(req)
	if err != nil {
		b.Fatal(err)
	}
	msgs := []Message{{Topic: testTopics().Requests, Key: []byte(req.SubmissionID), Value: payload}}
	commits := make([]Message, 0, 1)
	consumer := &fakeConsumer{}
	processor, err := NewProcessor(consumer, &fakeProducer{}, testTopics(), "worker", RunRequestHandlerFunc(func(context.Context, Message, contracts.RunRequest) error {
		return nil
	}), nil)
	if err != nil {
		b.Fatal(err)
	}
	processor.Now = fixedKafkaTime
	ctx, cancel := context.WithCancel(b.Context())
	defer cancel()
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for b.Loop() {
		consumer.messages = msgs
		consumer.committed = commits
		if err := processor.ProcessOnce(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
