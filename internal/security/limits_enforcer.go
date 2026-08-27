package security

import (
	"encoding/base64"
	"fmt"

	"overmindv/sandbox/internal/contracts"
)

type Guard struct {
	policy Policy
}

func NewGuard(policy Policy) *Guard {
	return &Guard{policy: policy.withDefaults()}
}

func (g *Guard) Policy() Policy {
	if g == nil {
		return DefaultPolicy()
	}
	return g.policy
}

func (g *Guard) ValidatePayload(payload []byte) error {
	policy := g.Policy()
	if len(payload) > policy.MaxPayloadBytes {
		return violation(CodePayloadTooLarge, "payload", fmt.Sprintf("payload is %d bytes, max is %d", len(payload), policy.MaxPayloadBytes))
	}
	return nil
}

func (g *Guard) ValidateRunRequest(req contracts.RunRequest) error {
	policy := g.Policy()
	if len(req.Code.Files)+len(req.Tests.Files) > policy.MaxFiles {
		return violation(CodeTooManyFiles, "files", fmt.Sprintf("request contains %d files, max is %d", len(req.Code.Files)+len(req.Tests.Files), policy.MaxFiles))
	}

	total, err := g.validateFiles("code.files", req.Code.Files, policy.MaxSourceFiles)
	if err != nil {
		return err
	}
	testTotal, err := g.validateFiles("tests.files", req.Tests.Files, policy.MaxTestFiles)
	if err != nil {
		return err
	}
	total += testTotal
	if total > policy.MaxPayloadBytes {
		return violation(CodePayloadTooLarge, "files", fmt.Sprintf("decoded files are %d bytes, max is %d", total, policy.MaxPayloadBytes))
	}

	if req.Code.Entrypoint != "" {
		if err := ValidateWorkspacePath(req.Code.Entrypoint, policy); err != nil {
			return err
		}
	}
	if req.Tests.Entrypoint != "" {
		if err := ValidateWorkspacePath(req.Tests.Entrypoint, policy); err != nil {
			return err
		}
	}
	return nil
}

func (g *Guard) validateFiles(field string, files []contracts.SourceFile, maxFiles int) (int, error) {
	policy := g.Policy()
	if len(files) > maxFiles {
		return 0, violation(CodeTooManyFiles, field, fmt.Sprintf("contains %d files, max is %d", len(files), maxFiles))
	}

	seen := make(map[string]struct{}, len(files))
	total := 0
	for index, file := range files {
		fieldName := fmt.Sprintf("%s[%d]", field, index)
		normalized, err := NormalizeRelativePath(file.Path, policy)
		if err != nil {
			return 0, err
		}
		if _, exists := seen[normalized]; exists {
			return 0, violation(CodeUnsafeFilename, fieldName+".path", "duplicate normalized path")
		}
		seen[normalized] = struct{}{}

		content, err := base64.StdEncoding.DecodeString(file.ContentB64)
		if err != nil {
			return 0, violation(CodeInvalidFileContent, fieldName+".content_b64", "content must be valid base64")
		}
		if len(content) > policy.MaxFileBytes {
			return 0, violation(CodeFileTooLarge, fieldName+".content_b64", fmt.Sprintf("file is %d bytes, max is %d", len(content), policy.MaxFileBytes))
		}
		if err := ValidateArchiveDisabled(normalized, content, policy); err != nil {
			return 0, err
		}
		total += len(content)
	}
	return total, nil
}
