package languages

import (
	"context"
	"errors"
	"fmt"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
)

var ErrUnsupportedLanguage = errors.New("unsupported language")

type UnsupportedLanguageError struct {
	LanguageID string
	Version    string
	Reason     string
}

func (e UnsupportedLanguageError) Error() string {
	if e.Version == "" {
		return fmt.Sprintf("%s: %s", ErrUnsupportedLanguage, e.LanguageID)
	}
	if e.Reason == "" {
		return fmt.Sprintf("%s: %s@%s", ErrUnsupportedLanguage, e.LanguageID, e.Version)
	}
	return fmt.Sprintf("%s: %s@%s: %s", ErrUnsupportedLanguage, e.LanguageID, e.Version, e.Reason)
}

func (e UnsupportedLanguageError) Unwrap() error {
	return ErrUnsupportedLanguage
}

type LanguageAdapter interface {
	Name() string
	BuildCommand(ctx context.Context, req contracts.RunRequest, resolved ResolvedLanguage) (execution.ExecutionCommand, error)
}

type ResolvedLanguage struct {
	Language config.LanguageConfig
	Version  config.LanguageVersionConfig
}

func (r ResolvedLanguage) SupportsTestMode(mode contracts.TestMode) bool {
	for _, supported := range r.Version.TestModes {
		if supported == string(mode) {
			return true
		}
	}
	return false
}
