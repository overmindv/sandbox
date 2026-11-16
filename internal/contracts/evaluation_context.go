package contracts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// EvaluationContext связывает результат с неизменяемой версией и входами запуска.
type EvaluationContext struct {
	TaskVersionID string `json:"task_version_id"`
	SuiteSHA256   string `json:"suite_sha256"`
}

// InputSuiteSHA256 хеширует упорядоченный JSON-массив stdin (Go json.Marshal).
// Эталоны остаются в tasks; их идентичность задаётся TaskVersionID.
func InputSuiteSHA256(inputs []string) string {
	payload, _ := json.Marshal(inputs)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// Контекст включается только для producer, явно поддерживающего расширение v1.
func EvaluationContextForRequest(req RunRequest) *EvaluationContext {
	if req.Metadata["result_context_version"] != "1" || req.Execution == nil {
		return nil
	}
	version, _ := req.Metadata["task_version_id"].(string)
	if !uuidPattern.MatchString(version) {
		return nil
	}
	return &EvaluationContext{TaskVersionID: version, SuiteSHA256: InputSuiteSHA256(req.Execution.Inputs)}
}
