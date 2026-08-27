package kafka

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"overmindv/sandbox/internal/contracts"
)

func DecodeRunRequest(value []byte) (contracts.RunRequest, error) {
	var req contracts.RunRequest
	if err := decodeStrict(value, &req); err != nil {
		return contracts.RunRequest{}, err
	}
	return req, nil
}

func EncodeRunRequest(req contracts.RunRequest) ([]byte, error) {
	return json.Marshal(req)
}

func EncodeRunEvent(event contracts.RunEvent) ([]byte, error) {
	return json.Marshal(event)
}

func DecodeRunEvent(value []byte) (contracts.RunEvent, error) {
	var event contracts.RunEvent
	if err := decodeStrict(value, &event); err != nil {
		return contracts.RunEvent{}, err
	}
	return event, nil
}

func EncodeRunResult(result contracts.RunResult) ([]byte, error) {
	return json.Marshal(result)
}

func DecodeRunResult(value []byte) (contracts.RunResult, error) {
	var result contracts.RunResult
	if err := decodeStrict(value, &result); err != nil {
		return contracts.RunResult{}, err
	}
	return result, nil
}

func EncodeRunDeadLetter(deadLetter contracts.RunDeadLetter) ([]byte, error) {
	return json.Marshal(deadLetter)
}

func DecodeRunDeadLetter(value []byte) (contracts.RunDeadLetter, error) {
	var deadLetter contracts.RunDeadLetter
	if err := decodeStrict(value, &deadLetter); err != nil {
		return contracts.RunDeadLetter{}, err
	}
	return deadLetter, nil
}

func decodeStrict(value []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("payload contains trailing data")
	}
	return nil
}
