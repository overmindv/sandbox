package config

import (
	"fmt"
	"os"
	"path/filepath"
)

const LimitsSchemaVersion = "sandbox.limits.v1"

type LimitsRegistryConfig struct {
	SchemaVersion string                           `yaml:"schema_version"`
	Global        GlobalLimitsConfig               `yaml:"global"`
	HardMax       ExecutionLimitsConfig            `yaml:"hard_max"`
	Profiles      map[string]ExecutionLimitsConfig `yaml:"profiles"`
	Policy        LimitsPolicyConfig               `yaml:"policy"`
}

type GlobalLimitsConfig struct {
	RequestMaxBytes       int `yaml:"request_max_bytes"`
	FileContentMaxBytes   int `yaml:"file_content_max_bytes"`
	SourceFilesMaxCount   int `yaml:"source_files_max_count"`
	TestFilesMaxCount     int `yaml:"test_files_max_count"`
	MetadataMaxProperties int `yaml:"metadata_max_properties"`
}

type ExecutionLimitsConfig struct {
	CPUms          int `yaml:"cpu_ms"`
	WallMS         int `yaml:"wall_ms"`
	MemoryMB       int `yaml:"memory_mb"`
	PIDs           int `yaml:"pids"`
	StdoutBytes    int `yaml:"stdout_bytes"`
	StderrBytes    int `yaml:"stderr_bytes"`
	WorkspaceBytes int `yaml:"workspace_bytes"`
}

type LimitsPolicyConfig struct {
	RequestLimitsMayOnlyDecreaseDefaults bool `yaml:"request_limits_may_only_decrease_defaults"`
	ClampToHardMax                       bool `yaml:"clamp_to_hard_max"`
	RejectWhenWallLessThanCPU            bool `yaml:"reject_when_wall_less_than_cpu"`
	RejectZeroMemory                     bool `yaml:"reject_zero_memory"`
	RejectZeroPIDs                       bool `yaml:"reject_zero_pids"`
	TruncateOutputOnLimit                bool `yaml:"truncate_output_on_limit"`
}

func LoadLimits(path string) (LimitsRegistryConfig, error) {
	resolvedPath, err := resolveConfigPath(path)
	if err != nil {
		return LimitsRegistryConfig{}, err
	}

	content, err := os.ReadFile(resolvedPath)
	if err != nil {
		return LimitsRegistryConfig{}, fmt.Errorf("read limits config %q: %w", path, err)
	}

	var cfg LimitsRegistryConfig
	if err := decodeYAML(content, &cfg); err != nil {
		return LimitsRegistryConfig{}, fmt.Errorf("parse limits config %q: %w", resolvedPath, err)
	}
	if err := cfg.Validate(); err != nil {
		return LimitsRegistryConfig{}, err
	}
	return cfg, nil
}

func (cfg LimitsRegistryConfig) Validate() error {
	if cfg.SchemaVersion != LimitsSchemaVersion {
		return fmt.Errorf("limits schema_version must be %q", LimitsSchemaVersion)
	}
	if cfg.Global.RequestMaxBytes <= 0 {
		return fmt.Errorf("limits global.request_max_bytes must be positive")
	}
	if cfg.Global.FileContentMaxBytes <= 0 {
		return fmt.Errorf("limits global.file_content_max_bytes must be positive")
	}
	if cfg.Global.SourceFilesMaxCount <= 0 {
		return fmt.Errorf("limits global.source_files_max_count must be positive")
	}
	if cfg.Global.TestFilesMaxCount <= 0 {
		return fmt.Errorf("limits global.test_files_max_count must be positive")
	}
	if cfg.HardMax.CPUms <= 0 || cfg.HardMax.WallMS <= 0 || cfg.HardMax.MemoryMB <= 0 || cfg.HardMax.PIDs <= 0 {
		return fmt.Errorf("limits hard_max cpu, wall, memory, and pids must be positive")
	}
	if cfg.HardMax.WallMS < cfg.HardMax.CPUms {
		return fmt.Errorf("limits hard_max.wall_ms must be greater than or equal to cpu_ms")
	}
	return nil
}

func resolveConfigPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("config path is required")
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
	return "", fmt.Errorf("config file %q not found", path)
}
