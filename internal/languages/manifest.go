package languages

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/security"
)

const ManifestSchemaVersion = "sandbox.languages.v1"

func LoadManifest(path string) (config.LanguageRegistryConfig, error) {
	resolvedPath, err := resolveManifestPath(path)
	if err != nil {
		return config.LanguageRegistryConfig{}, err
	}

	content, err := os.ReadFile(resolvedPath)
	if err != nil {
		return config.LanguageRegistryConfig{}, fmt.Errorf("read language manifest %q: %w", path, err)
	}

	var manifest config.LanguageRegistryConfig
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	if err := decoder.Decode(&manifest); err != nil {
		return config.LanguageRegistryConfig{}, fmt.Errorf("parse language manifest %q: %w", resolvedPath, err)
	}
	if err := ValidateManifest(manifest); err != nil {
		return config.LanguageRegistryConfig{}, err
	}
	return manifest, nil
}

func ValidateManifest(manifest config.LanguageRegistryConfig) error {
	if manifest.SchemaVersion != ManifestSchemaVersion {
		return fmt.Errorf("language manifest schema_version must be %q", ManifestSchemaVersion)
	}
	if manifest.Defaults.LanguageID == "" {
		return fmt.Errorf("language manifest default language_id is required")
	}
	if len(manifest.Languages) == 0 {
		return fmt.Errorf("language manifest must contain at least one language")
	}

	seenLanguages := make(map[string]struct{}, len(manifest.Languages))
	for _, language := range manifest.Languages {
		if language.ID == "" {
			return fmt.Errorf("language id is required")
		}
		if _, exists := seenLanguages[language.ID]; exists {
			return fmt.Errorf("duplicate language %q", language.ID)
		}
		seenLanguages[language.ID] = struct{}{}

		if language.Adapter == "" {
			return fmt.Errorf("language %q adapter is required", language.ID)
		}
		if len(language.Versions) == 0 {
			return fmt.Errorf("language %q must define at least one version", language.ID)
		}

		seenVersions := make(map[string]struct{}, len(language.Versions))
		defaultVersions := 0
		for _, version := range language.Versions {
			if version.Version == "" {
				return fmt.Errorf("language %q version is required", language.ID)
			}
			if _, exists := seenVersions[version.Version]; exists {
				return fmt.Errorf("duplicate language version %q@%s", language.ID, version.Version)
			}
			seenVersions[version.Version] = struct{}{}
			if version.Default {
				defaultVersions++
			}
			if version.RuntimeImage == "" {
				return fmt.Errorf("language %q@%s runtime_image is required", language.ID, version.Version)
			}
			if version.Execution.WorkingDir == "" {
				return fmt.Errorf("language %q@%s working_dir is required", language.ID, version.Version)
			}
			if len(version.Execution.Command.Argv) == 0 {
				return fmt.Errorf("language %q@%s command argv is required", language.ID, version.Version)
			}
			if err := security.ValidateRuntimeEnvironment(version.Execution.Environment); err != nil {
				return fmt.Errorf("language %q@%s environment: %w", language.ID, version.Version, err)
			}
		}
		if defaultVersions != 1 {
			return fmt.Errorf("language %q must define exactly one default version", language.ID)
		}
	}
	return nil
}

func resolveManifestPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("language manifest path is required")
	}
	clean := filepath.Clean(path)
	if _, err := os.Stat(clean); err == nil {
		return clean, nil
	}

	current, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for i := 0; i < 6; i++ {
		candidate := filepath.Join(current, clean)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return "", fmt.Errorf("language manifest %q not found", path)
}
