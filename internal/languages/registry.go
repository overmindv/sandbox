package languages

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
)

type Registry struct {
	manifest config.LanguageRegistryConfig
	adapters map[string]LanguageAdapter
}

func NewRegistry(manifest config.LanguageRegistryConfig, adapters ...LanguageAdapter) (*Registry, error) {
	if err := ValidateManifest(manifest); err != nil {
		return nil, err
	}

	registry := &Registry{
		manifest: manifest,
		adapters: make(map[string]LanguageAdapter, len(adapters)),
	}
	for _, adapter := range adapters {
		if adapter == nil {
			continue
		}
		name := adapter.Name()
		if name == "" {
			return nil, fmt.Errorf("language adapter name is required")
		}
		if _, exists := registry.adapters[name]; exists {
			return nil, fmt.Errorf("duplicate language adapter %q", name)
		}
		registry.adapters[name] = adapter
	}

	for _, language := range manifest.Languages {
		if language.Enabled {
			if _, exists := registry.adapters[language.Adapter]; !exists {
				return nil, fmt.Errorf("%w: %s uses missing adapter %q", ErrUnsupportedLanguage, language.ID, language.Adapter)
			}
		}
	}
	return registry, nil
}

func (r *Registry) Resolve(spec contracts.LanguageSpec, mode contracts.TestMode) (ResolvedLanguage, LanguageAdapter, error) {
	if r == nil {
		return ResolvedLanguage{}, nil, fmt.Errorf("%w: registry is nil", ErrUnsupportedLanguage)
	}

	language, ok := r.findLanguage(spec.ID)
	if !ok || !language.Enabled {
		return ResolvedLanguage{}, nil, UnsupportedLanguageError{LanguageID: spec.ID, Version: spec.Version, Reason: "language is not enabled"}
	}

	version, ok := findVersion(language, spec.Version)
	if !ok {
		return ResolvedLanguage{}, nil, UnsupportedLanguageError{LanguageID: spec.ID, Version: spec.Version, Reason: "version is not enabled"}
	}

	resolved := ResolvedLanguage{Language: language, Version: version}
	if !resolved.SupportsTestMode(mode) {
		return ResolvedLanguage{}, nil, UnsupportedLanguageError{LanguageID: spec.ID, Version: version.Version, Reason: fmt.Sprintf("test mode %q is not supported", mode)}
	}

	adapter, ok := r.adapters[language.Adapter]
	if !ok {
		return ResolvedLanguage{}, nil, UnsupportedLanguageError{LanguageID: spec.ID, Version: version.Version, Reason: fmt.Sprintf("adapter %q is missing", language.Adapter)}
	}
	return resolved, adapter, nil
}

// ResolveForExecution разрешает язык для execution-режима, не требуя test-mode.
func (r *Registry) ResolveForExecution(spec contracts.LanguageSpec) (ResolvedLanguage, LanguageAdapter, error) {
	if r == nil {
		return ResolvedLanguage{}, nil, fmt.Errorf("%w: registry is nil", ErrUnsupportedLanguage)
	}

	language, ok := r.findLanguage(spec.ID)
	if !ok || !language.Enabled {
		return ResolvedLanguage{}, nil, UnsupportedLanguageError{LanguageID: spec.ID, Version: spec.Version, Reason: "language is not enabled"}
	}
	version, ok := findVersion(language, spec.Version)
	if !ok {
		return ResolvedLanguage{}, nil, UnsupportedLanguageError{LanguageID: spec.ID, Version: spec.Version, Reason: "version is not enabled"}
	}
	adapter, ok := r.adapters[language.Adapter]
	if !ok {
		return ResolvedLanguage{}, nil, UnsupportedLanguageError{LanguageID: spec.ID, Version: version.Version, Reason: fmt.Sprintf("adapter %q is missing", language.Adapter)}
	}

	return ResolvedLanguage{Language: language, Version: version}, adapter, nil
}

func (r *Registry) BuildCommand(ctx context.Context, req contracts.RunRequest) (execution.ExecutionCommand, error) {
	if req.Execution != nil && req.Execution.Mode == contracts.ExecutionMode {
		resolved, adapter, err := r.ResolveForExecution(req.Language)
		if err != nil {
			return execution.ExecutionCommand{}, err
		}
		return adapter.BuildCommand(ctx, req, resolved)
	}
	resolved, adapter, err := r.Resolve(req.Language, req.Tests.Mode)
	if err != nil {
		return execution.ExecutionCommand{}, err
	}
	return adapter.BuildCommand(ctx, req, resolved)
}

func (r *Registry) EnabledLanguageIDs() []string {
	if r == nil {
		return nil
	}
	ids := make([]string, 0, len(r.manifest.Languages))
	for _, language := range r.manifest.Languages {
		if language.Enabled {
			ids = append(ids, language.ID)
		}
	}
	return ids
}

func (r *Registry) findLanguage(id string) (config.LanguageConfig, bool) {
	for _, language := range r.manifest.Languages {
		if language.ID == id {
			return language, true
		}
	}
	return config.LanguageConfig{}, false
}

func findVersion(language config.LanguageConfig, requested string) (config.LanguageVersionConfig, bool) {
	if requested == "" {
		for _, version := range language.Versions {
			if version.Default {
				return version, true
			}
		}
		return config.LanguageVersionConfig{}, false
	}

	for _, version := range language.Versions {
		if version.Version == requested {
			return version, true
		}
	}
	return config.LanguageVersionConfig{}, false
}

func ValidateAllowedExtensionsForAdapter(files []contracts.SourceFile, allowed []string) error {
	if len(allowed) == 0 {
		return nil
	}

	allowedSet := make(map[string]struct{}, len(allowed))
	for _, ext := range allowed {
		allowedSet[strings.ToLower(ext)] = struct{}{}
	}

	for _, file := range files {
		ext := strings.ToLower(filepath.Ext(file.Path))
		if _, ok := allowedSet[ext]; !ok {
			return fmt.Errorf("file %q extension %q is not allowed", file.Path, ext)
		}
	}
	return nil
}
