package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadLocalConfig(t *testing.T) {
	clearSandboxEnv(t)

	cfg, err := Load(filepath.Join("..", "..", "configs", "local.yaml"))
	if err != nil {
		t.Errorf("load local config: %v", err)
	}

	if cfg.App.Environment != "local" {
		t.Errorf("expected local environment, got %q", cfg.App.Environment)
	}
	if cfg.Executor.Type != "fake" {
		t.Errorf("expected fake executor, got %q", cfg.Executor.Type)
	}
}

func TestLoadAppliesEnvOverrides(t *testing.T) {
	clearSandboxEnv(t)
	t.Setenv("SANDBOX_EXECUTOR_TYPE", "gojudge")
	t.Setenv("SANDBOX_FEATURE_FAKE_EXECUTOR", "false")
	t.Setenv("SANDBOX_FEATURE_GOJUDGE_EXECUTOR", "true")
	t.Setenv("SANDBOX_GOJUDGE_BASE_URL", "http://gojudge.test")
	t.Setenv("SANDBOX_KAFKA_BROKERS", "kafka-1:9092,kafka-2:9092")

	cfg, err := Load(filepath.Join("..", "..", "configs", "local.yaml"))
	if err != nil {
		t.Errorf("load config with env overrides: %v", err)
	}

	if cfg.Executor.Type != "gojudge" {
		t.Errorf("expected env executor override, got %q", cfg.Executor.Type)
	}
	if cfg.Executor.GoJudge.BaseURL != "http://gojudge.test" {
		t.Errorf("expected env gojudge URL override, got %q", cfg.Executor.GoJudge.BaseURL)
	}
	if len(cfg.Kafka.Brokers) != 2 {
		t.Errorf("expected two kafka brokers, got %#v", cfg.Kafka.Brokers)
	}
}

func TestLoadLimitsConfig(t *testing.T) {
	limits, err := LoadLimits(filepath.Join("..", "..", "configs", "limits.yaml"))
	if err != nil {
		t.Errorf("load limits config: %v", err)
		return
	}

	if limits.SchemaVersion != LimitsSchemaVersion {
		t.Errorf("expected limits schema %q, got %q", LimitsSchemaVersion, limits.SchemaVersion)
	}
	if limits.Global.RequestMaxBytes != 2097152 {
		t.Errorf("expected request max bytes from config, got %d", limits.Global.RequestMaxBytes)
	}
	if limits.HardMax.MemoryMB != 1024 {
		t.Errorf("expected hard max memory from config, got %d", limits.HardMax.MemoryMB)
	}
}

func clearSandboxEnv(t *testing.T) {
	t.Helper()
	original := make(map[string]string)
	for _, env := range os.Environ() {
		key, _, ok := strings.Cut(env, "=")
		if ok && strings.HasPrefix(key, "SANDBOX_") {
			original[key] = os.Getenv(key)
			if err := os.Unsetenv(key); err != nil {
				t.Errorf("unset %s: %v", key, err)
			}
		}
	}
	t.Cleanup(func() {
		for key, value := range original {
			_ = os.Setenv(key, value)
		}
	})
}
