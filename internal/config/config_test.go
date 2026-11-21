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
	t.Setenv("SANDBOX_SHUTDOWN_TIMEOUT_MS", "24000")
	t.Setenv("SANDBOX_GOJUDGE_BASE_URL", "https://gojudge.test")
	t.Setenv("SANDBOX_GOJUDGE_COMPILER_URL", "https://compiler.test")
	t.Setenv("SANDBOX_GOJUDGE_TLS_CA_FILE", "/tls/runner-ca.pem")
	t.Setenv("SANDBOX_GOJUDGE_TLS_CERT_FILE", "/tls/worker.pem")
	t.Setenv("SANDBOX_GOJUDGE_TLS_KEY_FILE", "/tls/worker-key.pem")
	t.Setenv("SANDBOX_KAFKA_BROKERS", "kafka-1:9092,kafka-2:9092")
	t.Setenv("SANDBOX_ARTIFACTS_PROVIDER", "filesystem")
	t.Setenv("SANDBOX_ARTIFACTS_ROOT_DIR", ".test-artifacts")
	t.Setenv("SANDBOX_ARTIFACTS_INLINE_MAX_BYTES", "123")
	t.Setenv("SANDBOX_TESTS_CACHE_TTL_MS", "456")

	cfg, err := Load(filepath.Join("..", "..", "configs", "local.yaml"))
	if err != nil {
		t.Errorf("load config with env overrides: %v", err)
	}

	if cfg.Executor.Type != "gojudge" {
		t.Errorf("expected env executor override, got %q", cfg.Executor.Type)
	}
	if cfg.Executor.GoJudge.BaseURL != "https://gojudge.test" {
		t.Errorf("expected env gojudge URL override, got %q", cfg.Executor.GoJudge.BaseURL)
	}
	if cfg.App.ShutdownTimeoutMS != 24000 {
		t.Errorf("expected shutdown timeout override, got %d", cfg.App.ShutdownTimeoutMS)
	}
	if cfg.Executor.GoJudge.CompilerURL != "https://compiler.test" {
		t.Error("go-judge compiler URL did not come from the environment")
	}
	wantTLS := GoJudgeTLS{CAFile: "/tls/runner-ca.pem", CertFile: "/tls/worker.pem", KeyFile: "/tls/worker-key.pem"}
	if cfg.Executor.GoJudge.TLS != wantTLS {
		t.Error("go-judge TLS paths did not come from the environment")
	}
	if len(cfg.Kafka.Brokers) != 2 {
		t.Errorf("expected two kafka brokers, got %#v", cfg.Kafka.Brokers)
	}
	if cfg.Storage.Artifacts.RootDir != ".test-artifacts" {
		t.Errorf("expected artifact root env override, got %q", cfg.Storage.Artifacts.RootDir)
	}
	if cfg.Storage.Artifacts.InlineMaxBytes != 123 {
		t.Errorf("expected artifact inline max env override, got %d", cfg.Storage.Artifacts.InlineMaxBytes)
	}
	if cfg.Tests.ObjectStorage.CacheTTLMS != 456 {
		t.Errorf("expected tests cache ttl env override, got %d", cfg.Tests.ObjectStorage.CacheTTLMS)
	}
}

func TestLoadRejectsPartialGoJudgeTLS(t *testing.T) {
	clearSandboxEnv(t)
	t.Setenv("SANDBOX_EXECUTOR_TYPE", "gojudge")
	t.Setenv("SANDBOX_FEATURE_GOJUDGE_EXECUTOR", "true")
	t.Setenv("SANDBOX_GOJUDGE_TLS_CA_FILE", "/tls/runner-ca.pem")

	_, err := Load(filepath.Join("..", "..", "configs", "local.yaml"))
	if err == nil || !strings.Contains(err.Error(), "requires ca_file, cert_file and key_file together") {
		t.Errorf("partial TLS configuration was not rejected: %v", err)
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

func TestProductionRequiresDurableStorage(t *testing.T) {
	for _, env := range []string{"prod", "production"} {
		cfg := Default()
		cfg.App.Environment = env
		cfg.Storage.Type = "memory"
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "requires postgres") {
			t.Fatalf("%s accepted volatile storage: %v", env, err)
		}
	}
}

func TestSuiteAndArtifactCredentialsDiffer(t *testing.T) {
	cfg := Default()
	cfg.Tests.ObjectStorage.Enabled = true
	cfg.Tests.ObjectStorage.Provider = "s3"
	cfg.Tests.ObjectStorage.AccessKeyID = "same-principal"
	cfg.Storage.Artifacts.Enabled = true
	cfg.Storage.Artifacts.Provider = "s3"
	cfg.Storage.Artifacts.AccessKeyID = " same-principal "
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "credentials must differ") {
		t.Fatalf("shared credentials accepted: %v", err)
	}
}

func TestProductionRejectsLegacyHarness(t *testing.T) {
	cfg := Default()
	cfg.App.Environment = "prod"
	cfg.FeatureFlags.LegacyHarness = true
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "legacy harness") {
		t.Fatalf("unsafe harness enabled: %v", err)
	}
}
