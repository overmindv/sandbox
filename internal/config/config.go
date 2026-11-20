package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	ConfigSchemaVersion = "sandbox.config.v1"
	DefaultConfigFile   = "configs/local.yaml"
)

var envPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-([^}]*))?\}`)

type Config struct {
	SchemaVersion string              `yaml:"schema_version"`
	App           AppConfig           `yaml:"app"`
	Config        ConfigPaths         `yaml:"config"`
	Server        ServerConfig        `yaml:"server"`
	FeatureFlags  FeatureFlags        `yaml:"feature_flags"`
	Kafka         KafkaConfig         `yaml:"kafka"`
	Scheduler     SchedulerConfig     `yaml:"scheduler"`
	Executor      ExecutorConfig      `yaml:"executor"`
	Tests         TestsConfig         `yaml:"tests"`
	Storage       StorageConfig       `yaml:"storage"`
	Security      SecurityConfig      `yaml:"security"`
	Observability ObservabilityConfig `yaml:"observability"`
}

type AppConfig struct {
	Name              string `yaml:"name"`
	Environment       string `yaml:"environment"`
	WorkerID          string `yaml:"worker_id"`
	ShutdownTimeoutMS int    `yaml:"shutdown_timeout_ms"`
}

type ConfigPaths struct {
	LanguagesFile    string `yaml:"languages_file"`
	LimitsFile       string `yaml:"limits_file"`
	GoJudgeMountFile string `yaml:"gojudge_mount_file"`
}

type ServerConfig struct {
	HealthAddr  string `yaml:"health_addr"`
	MetricsAddr string `yaml:"metrics_addr"`
}

type FeatureFlags struct {
	LegacyHarness          bool `yaml:"legacy_harness"`
	FakeExecutor           bool `yaml:"fake_executor"`
	GoJudgeExecutor        bool `yaml:"gojudge_executor"`
	InlineTests            bool `yaml:"inline_tests"`
	ObjectStorageTest      bool `yaml:"object_storage_tests"`
	ObjectStorageArtifacts bool `yaml:"object_storage_artifacts"`
}

type KafkaConfig struct {
	Brokers  []string            `yaml:"brokers"`
	ClientID string              `yaml:"client_id"`
	GroupID  string              `yaml:"group_id"`
	Topics   KafkaTopics         `yaml:"topics"`
	Consumer KafkaConsumerConfig `yaml:"consumer"`
	Producer KafkaProducerConfig `yaml:"producer"`
	Retry    KafkaRetryConfig    `yaml:"retry"`
	Security KafkaSecurityConfig `yaml:"security"`
}

type KafkaTopics struct {
	Requests   string `yaml:"requests"`
	Retry      string `yaml:"retry"`
	Events     string `yaml:"events"`
	Results    string `yaml:"results"`
	DeadLetter string `yaml:"deadletter"`
}

type KafkaConsumerConfig struct {
	AutoOffsetReset          string `yaml:"auto_offset_reset"`
	SessionTimeoutMS         int    `yaml:"session_timeout_ms"`
	HeartbeatIntervalMS      int    `yaml:"heartbeat_interval_ms"`
	MaxPollRecords           int    `yaml:"max_poll_records"`
	CommitAfterResultPublish bool   `yaml:"commit_after_result_publish"`
}

type KafkaProducerConfig struct {
	Acks             string `yaml:"acks"`
	Idempotent       bool   `yaml:"idempotent"`
	Compression      string `yaml:"compression"`
	LingerMS         int    `yaml:"linger_ms"`
	RequestTimeoutMS int    `yaml:"request_timeout_ms"`
}

type KafkaRetryConfig struct {
	Enabled              bool    `yaml:"enabled"`
	MaxAttempts          int     `yaml:"max_attempts"`
	InitialBackoffMS     int     `yaml:"initial_backoff_ms"`
	MaxBackoffMS         int     `yaml:"max_backoff_ms"`
	BackoffMultiplier    float64 `yaml:"backoff_multiplier"`
	EarlyRetrySleepCapMS int     `yaml:"early_retry_sleep_cap_ms"`
}

type SchedulerConfig struct {
	MaxInFlight            int                    `yaml:"max_in_flight"`
	JobTimeoutMS           int                    `yaml:"job_timeout_ms"`
	MaxInFlightPerLanguage map[string]int         `yaml:"max_in_flight_per_language"`
	Queue                  SchedulerQueueConfig   `yaml:"queue"`
	Admission              SchedulerAdmissionConf `yaml:"admission"`
}

type SchedulerQueueConfig struct {
	Policy           string `yaml:"policy"`
	MaxDepth         int    `yaml:"max_depth"`
	EnqueueTimeoutMS int    `yaml:"enqueue_timeout_ms"`
}

type SchedulerAdmissionConf struct {
	RejectWhenExecutorUnhealthy bool `yaml:"reject_when_executor_unhealthy"`
	MaxQueueAgeMS               int  `yaml:"max_queue_age_ms"`
}

type ExecutorConfig struct {
	Type            string        `yaml:"type"`
	ClientTimeoutMS int           `yaml:"client_timeout_ms"`
	Fake            FakeExecutor  `yaml:"fake"`
	GoJudge         GoJudgeClient `yaml:"gojudge"`
}

type FakeExecutor struct {
	DefaultStatus       string `yaml:"default_status"`
	LatencyMS           int    `yaml:"latency_ms"`
	EchoRequestMetadata bool   `yaml:"echo_request_metadata"`
}

type GoJudgeClient struct {
	BaseURL            string `yaml:"base_url"`
	RequestTimeoutMS   int    `yaml:"request_timeout_ms"`
	MaxIdleConnections int    `yaml:"max_idle_connections"`
}

type TestsConfig struct {
	Inline        InlineTestsConfig        `yaml:"inline"`
	ObjectStorage ObjectStorageTestsConfig `yaml:"object_storage"`
}

type InlineTestsConfig struct {
	Enabled       bool `yaml:"enabled"`
	MaxFiles      int  `yaml:"max_files"`
	MaxTotalBytes int  `yaml:"max_total_bytes"`
}

type ObjectStorageTestsConfig struct {
	Enabled          bool   `yaml:"enabled"`
	Provider         string `yaml:"provider"`
	Bucket           string `yaml:"bucket"`
	Endpoint         string `yaml:"endpoint"`
	Region           string `yaml:"region"`
	AccessKeyID      string `yaml:"access_key_id"`
	SecretAccessKey  string `yaml:"secret_access_key"`
	KeyPrefix        string `yaml:"key_prefix"`
	CacheTTLMS       int    `yaml:"cache_ttl_ms"`
	MaxBytes         int    `yaml:"max_bytes"`
	RequestTimeoutMS int    `yaml:"request_timeout_ms"`
	ForcePathStyle   bool   `yaml:"force_path_style"`
}

type StorageConfig struct {
	Type      string                 `yaml:"type"`
	Postgres  PostgresStore          `yaml:"postgres"`
	Artifacts ArtifactStorageConfig  `yaml:"artifacts"`
	Retention StorageRetentionConfig `yaml:"retention"`
}

type PostgresStore struct {
	DSN                string `yaml:"dsn"`
	MaxOpenConnections int    `yaml:"max_open_connections"`
	MaxIdleConnections int    `yaml:"max_idle_connections"`
}

type ArtifactStorageConfig struct {
	Enabled          bool   `yaml:"enabled"`
	Provider         string `yaml:"provider"`
	Bucket           string `yaml:"bucket"`
	Endpoint         string `yaml:"endpoint"`
	Region           string `yaml:"region"`
	AccessKeyID      string `yaml:"access_key_id"`
	SecretAccessKey  string `yaml:"secret_access_key"`
	RootDir          string `yaml:"root_dir"`
	KeyPrefix        string `yaml:"key_prefix"`
	InlineMaxBytes   int    `yaml:"inline_max_bytes"`
	MaxBytes         int    `yaml:"max_bytes"`
	RequestTimeoutMS int    `yaml:"request_timeout_ms"`
	ForcePathStyle   bool   `yaml:"force_path_style"`
}

type StorageRetentionConfig struct {
	CompletedRunsDays      int `yaml:"completed_runs_days"`
	ArtifactsDays          int `yaml:"artifacts_days"`
	PublishedOutboxDays    int `yaml:"published_outbox_days"`
	FailedOutboxDays       int `yaml:"failed_outbox_days"`
	IdempotencyKeysDays    int `yaml:"idempotency_keys_days"`
	WorkerHeartbeatStaleMS int `yaml:"worker_heartbeat_stale_ms"`
}

type SecurityConfig struct {
	Network    NetworkSecurityConfig    `yaml:"network"`
	Filesystem FilesystemSecurityConfig `yaml:"filesystem"`
	Logs       LogSecurityConfig        `yaml:"logs"`
}

type NetworkSecurityConfig struct {
	AllowUserCodeNetwork bool `yaml:"allow_user_code_network"`
	DenyMetadataEndpoint bool `yaml:"deny_metadata_endpoints"`
}

type FilesystemSecurityConfig struct {
	AllowAbsolutePaths  bool `yaml:"allow_absolute_paths"`
	AllowParentSegments bool `yaml:"allow_parent_segments"`
	AllowSymlinks       bool `yaml:"allow_symlinks"`
	MaxFiles            int  `yaml:"max_files"`
}

type LogSecurityConfig struct {
	RedactSecrets  bool `yaml:"redact_secrets"`
	MaxStdoutBytes int  `yaml:"max_stdout_bytes"`
	MaxStderrBytes int  `yaml:"max_stderr_bytes"`
}

type ObservabilityConfig struct {
	LogLevel       string `yaml:"log_level"`
	LogFormat      string `yaml:"log_format"`
	MetricsEnabled bool   `yaml:"metrics_enabled"`
	TracingEnabled bool   `yaml:"tracing_enabled"`
	AuditEnabled   bool   `yaml:"audit_enabled"`
}

func Default() Config {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "sandbox-worker"
	}

	return Config{
		SchemaVersion: ConfigSchemaVersion,
		App: AppConfig{
			Name:              "sandbox-worker",
			Environment:       "local",
			WorkerID:          hostname,
			ShutdownTimeoutMS: 15000,
		},
		Config: ConfigPaths{
			LanguagesFile:    "configs/languages.yaml",
			LimitsFile:       "configs/limits.yaml",
			GoJudgeMountFile: "configs/gojudge-mount.yaml",
		},
		Server: ServerConfig{
			HealthAddr:  "0.0.0.0:8080",
			MetricsAddr: "0.0.0.0:9090",
		},
		FeatureFlags: FeatureFlags{
			FakeExecutor:           true,
			GoJudgeExecutor:        false,
			InlineTests:            true,
			ObjectStorageTest:      false,
			ObjectStorageArtifacts: true,
		},
		Kafka: KafkaConfig{
			Brokers:  []string{"localhost:29092"},
			ClientID: "sandbox-worker-local",
			GroupID:  "sandbox-code-execution-v1",
			Topics: KafkaTopics{
				Requests:   "code-execution.requests.v1",
				Retry:      "code-execution.retry.v1",
				Events:     "code-execution.events.v1",
				Results:    "code-execution.results.v1",
				DeadLetter: "code-execution.deadletter.v1",
			},
			Retry: KafkaRetryConfig{
				Enabled:              true,
				MaxAttempts:          3,
				InitialBackoffMS:     500,
				MaxBackoffMS:         30000,
				BackoffMultiplier:    2,
				EarlyRetrySleepCapMS: 5000,
			},
		},
		Scheduler: SchedulerConfig{
			JobTimeoutMS:           10000,
			MaxInFlight:            4,
			MaxInFlightPerLanguage: map[string]int{"python": 4},
			Queue: SchedulerQueueConfig{
				Policy:           "bounded_fifo",
				MaxDepth:         16,
				EnqueueTimeoutMS: 100,
			},
			Admission: SchedulerAdmissionConf{
				RejectWhenExecutorUnhealthy: true,
				MaxQueueAgeMS:               300000,
			},
		},
		Executor: ExecutorConfig{
			Type:            "fake",
			ClientTimeoutMS: 10000,
			Fake: FakeExecutor{
				DefaultStatus:       "passed",
				LatencyMS:           50,
				EchoRequestMetadata: true,
			},
			GoJudge: GoJudgeClient{
				BaseURL:            "http://localhost:5050",
				RequestTimeoutMS:   10000,
				MaxIdleConnections: 8,
			},
		},
		Tests: TestsConfig{
			Inline: InlineTestsConfig{
				Enabled:       true,
				MaxFiles:      16,
				MaxTotalBytes: 524288,
			},
			ObjectStorage: ObjectStorageTestsConfig{
				Provider:         "disabled",
				KeyPrefix:        "sandbox-tests",
				MaxBytes:         1048576,
				RequestTimeoutMS: 10000,
				ForcePathStyle:   true,
			},
		},
		Storage: StorageConfig{
			Type: "memory",
			Artifacts: ArtifactStorageConfig{
				Enabled:          true,
				Provider:         "filesystem",
				RootDir:          ".sandbox-artifacts",
				KeyPrefix:        "sandbox-artifacts",
				InlineMaxBytes:   4096,
				MaxBytes:         1048576,
				RequestTimeoutMS: 10000,
				ForcePathStyle:   true,
			},
			Retention: StorageRetentionConfig{
				CompletedRunsDays:      7,
				ArtifactsDays:          7,
				PublishedOutboxDays:    3,
				FailedOutboxDays:       30,
				IdempotencyKeysDays:    30,
				WorkerHeartbeatStaleMS: 120000,
			},
		},
		Security: SecurityConfig{
			Network: NetworkSecurityConfig{
				AllowUserCodeNetwork: false,
				DenyMetadataEndpoint: true,
			},
			Filesystem: FilesystemSecurityConfig{
				MaxFiles: 64,
			},
			Logs: LogSecurityConfig{
				RedactSecrets:  true,
				MaxStdoutBytes: 32768,
				MaxStderrBytes: 32768,
			},
		},
		Observability: ObservabilityConfig{
			LogLevel:       "info",
			LogFormat:      "json",
			MetricsEnabled: true,
			AuditEnabled:   true,
		},
	}
}

func LoadFromEnv() (Config, error) {
	return Load(os.Getenv("SANDBOX_CONFIG_FILE"))
}

func Load(path string) (Config, error) {
	if strings.TrimSpace(path) == "" {
		path = DefaultConfigFile
	}

	cfg := Default()
	content, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return Config{}, fmt.Errorf("read config %q: %w", path, err)
	}

	expanded := expandEnvDefaults(string(content))
	if err := decodeYAML([]byte(expanded), &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %q: %w", path, err)
	}
	if err := applyEnvOverrides(&cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func decodeYAML(data []byte, target any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	return decoder.Decode(target)
}

func expandEnvDefaults(input string) string {
	return envPattern.ReplaceAllStringFunc(input, func(match string) string {
		parts := envPattern.FindStringSubmatch(match)
		if len(parts) == 0 {
			return match
		}

		if value, ok := os.LookupEnv(parts[1]); ok {
			return value
		}
		if len(parts) >= 4 {
			return parts[3]
		}
		return ""
	})
}

func applyEnvOverrides(cfg *Config) error {
	cfg.Kafka.Security.applyEnv()
	if value := os.Getenv("SANDBOX_ENV"); value != "" {
		cfg.App.Environment = value
	}
	if value := os.Getenv("SANDBOX_WORKER_ID"); value != "" {
		cfg.App.WorkerID = value
	}
	if value := os.Getenv("SANDBOX_KAFKA_BROKERS"); value != "" {
		cfg.Kafka.Brokers = splitCSV(value)
	}
	if value := os.Getenv("SANDBOX_KAFKA_CLIENT_ID"); value != "" {
		cfg.Kafka.ClientID = value
	}
	if value := os.Getenv("SANDBOX_KAFKA_GROUP_ID"); value != "" {
		cfg.Kafka.GroupID = value
	}
	if value := os.Getenv("SANDBOX_KAFKA_TOPIC_REQUESTS"); value != "" {
		cfg.Kafka.Topics.Requests = value
	}
	if value := os.Getenv("SANDBOX_KAFKA_TOPIC_RETRY"); value != "" {
		cfg.Kafka.Topics.Retry = value
	}
	if value := os.Getenv("SANDBOX_KAFKA_TOPIC_EVENTS"); value != "" {
		cfg.Kafka.Topics.Events = value
	}
	if value := os.Getenv("SANDBOX_KAFKA_TOPIC_RESULTS"); value != "" {
		cfg.Kafka.Topics.Results = value
	}
	if value := os.Getenv("SANDBOX_KAFKA_TOPIC_DEADLETTER"); value != "" {
		cfg.Kafka.Topics.DeadLetter = value
	}
	if err := applyBoolEnv("SANDBOX_KAFKA_RETRY_ENABLED", &cfg.Kafka.Retry.Enabled); err != nil {
		return err
	}
	if err := applyIntEnv("SANDBOX_KAFKA_RETRY_MAX_ATTEMPTS", &cfg.Kafka.Retry.MaxAttempts); err != nil {
		return err
	}
	if err := applyIntEnv("SANDBOX_KAFKA_RETRY_INITIAL_BACKOFF_MS", &cfg.Kafka.Retry.InitialBackoffMS); err != nil {
		return err
	}
	if err := applyIntEnv("SANDBOX_KAFKA_RETRY_MAX_BACKOFF_MS", &cfg.Kafka.Retry.MaxBackoffMS); err != nil {
		return err
	}
	if err := applyFloatEnv("SANDBOX_KAFKA_RETRY_BACKOFF_MULTIPLIER", &cfg.Kafka.Retry.BackoffMultiplier); err != nil {
		return err
	}
	if err := applyIntEnv("SANDBOX_KAFKA_RETRY_EARLY_SLEEP_CAP_MS", &cfg.Kafka.Retry.EarlyRetrySleepCapMS); err != nil {
		return err
	}
	if value := os.Getenv("SANDBOX_EXECUTOR_TYPE"); value != "" {
		cfg.Executor.Type = value
	}
	if value := os.Getenv("SANDBOX_GOJUDGE_BASE_URL"); value != "" {
		cfg.Executor.GoJudge.BaseURL = value
	}
	if err := applyIntEnv("SANDBOX_EXECUTOR_CLIENT_TIMEOUT_MS", &cfg.Executor.ClientTimeoutMS); err != nil {
		return err
	}
	if err := applyBoolEnv("SANDBOX_FEATURE_FAKE_EXECUTOR", &cfg.FeatureFlags.FakeExecutor); err != nil {
		return err
	}
	if err := applyBoolEnv("SANDBOX_FEATURE_GOJUDGE_EXECUTOR", &cfg.FeatureFlags.GoJudgeExecutor); err != nil {
		return err
	}
	if err := applyBoolEnv("SANDBOX_FEATURE_LEGACY_HARNESS", &cfg.FeatureFlags.LegacyHarness); err != nil {
		return err
	}
	if err := applyBoolEnv("SANDBOX_FEATURE_INLINE_TESTS", &cfg.FeatureFlags.InlineTests); err != nil {
		return err
	}
	if err := applyBoolEnv("SANDBOX_FEATURE_OBJECT_STORAGE_TESTS", &cfg.FeatureFlags.ObjectStorageTest); err != nil {
		return err
	}
	if err := applyBoolEnv("SANDBOX_FEATURE_OBJECT_STORAGE_ARTIFACTS", &cfg.FeatureFlags.ObjectStorageArtifacts); err != nil {
		return err
	}
	if err := applyIntEnv("SANDBOX_MAX_IN_FLIGHT", &cfg.Scheduler.MaxInFlight); err != nil {
		return err
	}
	if err := applyMapIntEnv("SANDBOX_PYTHON_MAX_IN_FLIGHT", cfg.Scheduler.MaxInFlightPerLanguage, "python"); err != nil {
		return err
	}
	if err := applyIntEnv("SANDBOX_SCHEDULER_JOB_TIMEOUT_MS", &cfg.Scheduler.JobTimeoutMS); err != nil {
		return err
	}
	if err := applyIntEnv("SANDBOX_SCHEDULER_QUEUE_MAX_DEPTH", &cfg.Scheduler.Queue.MaxDepth); err != nil {
		return err
	}
	if value := os.Getenv("SANDBOX_TESTS_PROVIDER"); value != "" {
		cfg.Tests.ObjectStorage.Provider = value
	}
	if value := os.Getenv("SANDBOX_TESTS_BUCKET"); value != "" {
		cfg.Tests.ObjectStorage.Bucket = value
	}
	if value := os.Getenv("SANDBOX_TESTS_ENDPOINT"); value != "" {
		cfg.Tests.ObjectStorage.Endpoint = value
	}
	if value := os.Getenv("SANDBOX_TESTS_REGION"); value != "" {
		cfg.Tests.ObjectStorage.Region = value
	}
	if value := os.Getenv("SANDBOX_TESTS_ACCESS_KEY_ID"); value != "" {
		cfg.Tests.ObjectStorage.AccessKeyID = value
	}
	if value := os.Getenv("SANDBOX_TESTS_SECRET_ACCESS_KEY"); value != "" {
		cfg.Tests.ObjectStorage.SecretAccessKey = value
	}
	if value := os.Getenv("SANDBOX_TESTS_KEY_PREFIX"); value != "" {
		cfg.Tests.ObjectStorage.KeyPrefix = value
	}
	if err := applyIntEnv("SANDBOX_TESTS_CACHE_TTL_MS", &cfg.Tests.ObjectStorage.CacheTTLMS); err != nil {
		return err
	}
	if err := applyIntEnv("SANDBOX_TESTS_OBJECT_MAX_BYTES", &cfg.Tests.ObjectStorage.MaxBytes); err != nil {
		return err
	}
	if err := applyIntEnv("SANDBOX_TESTS_REQUEST_TIMEOUT_MS", &cfg.Tests.ObjectStorage.RequestTimeoutMS); err != nil {
		return err
	}
	if err := applyBoolEnv("SANDBOX_TESTS_FORCE_PATH_STYLE", &cfg.Tests.ObjectStorage.ForcePathStyle); err != nil {
		return err
	}
	if value := os.Getenv("SANDBOX_STORAGE_TYPE"); value != "" {
		cfg.Storage.Type = value
	}
	if value := os.Getenv("SANDBOX_POSTGRES_DSN"); value != "" {
		cfg.Storage.Postgres.DSN = value
	}
	if err := applyBoolEnv("SANDBOX_ARTIFACTS_ENABLED", &cfg.Storage.Artifacts.Enabled); err != nil {
		return err
	}
	if value := os.Getenv("SANDBOX_ARTIFACTS_PROVIDER"); value != "" {
		cfg.Storage.Artifacts.Provider = value
	}
	if value := os.Getenv("SANDBOX_ARTIFACTS_BUCKET"); value != "" {
		cfg.Storage.Artifacts.Bucket = value
	}
	if value := os.Getenv("SANDBOX_ARTIFACTS_ENDPOINT"); value != "" {
		cfg.Storage.Artifacts.Endpoint = value
	}
	if value := os.Getenv("SANDBOX_ARTIFACTS_REGION"); value != "" {
		cfg.Storage.Artifacts.Region = value
	}
	if value := os.Getenv("SANDBOX_ARTIFACTS_ACCESS_KEY_ID"); value != "" {
		cfg.Storage.Artifacts.AccessKeyID = value
	}
	if value := os.Getenv("SANDBOX_ARTIFACTS_SECRET_ACCESS_KEY"); value != "" {
		cfg.Storage.Artifacts.SecretAccessKey = value
	}
	if value := os.Getenv("SANDBOX_ARTIFACTS_ROOT_DIR"); value != "" {
		cfg.Storage.Artifacts.RootDir = value
	}
	if value := os.Getenv("SANDBOX_ARTIFACTS_KEY_PREFIX"); value != "" {
		cfg.Storage.Artifacts.KeyPrefix = value
	}
	if err := applyIntEnv("SANDBOX_ARTIFACTS_INLINE_MAX_BYTES", &cfg.Storage.Artifacts.InlineMaxBytes); err != nil {
		return err
	}
	if err := applyIntEnv("SANDBOX_ARTIFACTS_MAX_BYTES", &cfg.Storage.Artifacts.MaxBytes); err != nil {
		return err
	}
	if err := applyIntEnv("SANDBOX_ARTIFACTS_REQUEST_TIMEOUT_MS", &cfg.Storage.Artifacts.RequestTimeoutMS); err != nil {
		return err
	}
	if err := applyBoolEnv("SANDBOX_ARTIFACTS_FORCE_PATH_STYLE", &cfg.Storage.Artifacts.ForcePathStyle); err != nil {
		return err
	}
	if err := applyIntEnv("SANDBOX_RETENTION_COMPLETED_RUNS_DAYS", &cfg.Storage.Retention.CompletedRunsDays); err != nil {
		return err
	}
	if err := applyIntEnv("SANDBOX_RETENTION_ARTIFACTS_DAYS", &cfg.Storage.Retention.ArtifactsDays); err != nil {
		return err
	}
	if err := applyIntEnv("SANDBOX_RETENTION_PUBLISHED_OUTBOX_DAYS", &cfg.Storage.Retention.PublishedOutboxDays); err != nil {
		return err
	}
	if err := applyIntEnv("SANDBOX_RETENTION_FAILED_OUTBOX_DAYS", &cfg.Storage.Retention.FailedOutboxDays); err != nil {
		return err
	}
	if err := applyIntEnv("SANDBOX_RETENTION_IDEMPOTENCY_KEYS_DAYS", &cfg.Storage.Retention.IdempotencyKeysDays); err != nil {
		return err
	}
	if err := applyIntEnv("SANDBOX_WORKER_HEARTBEAT_STALE_MS", &cfg.Storage.Retention.WorkerHeartbeatStaleMS); err != nil {
		return err
	}
	if err := applyBoolEnv("SANDBOX_ALLOW_USER_CODE_NETWORK", &cfg.Security.Network.AllowUserCodeNetwork); err != nil {
		return err
	}
	if err := applyBoolEnv("SANDBOX_DENY_METADATA_ENDPOINTS", &cfg.Security.Network.DenyMetadataEndpoint); err != nil {
		return err
	}
	if err := applyBoolEnv("SANDBOX_REDACT_SECRETS", &cfg.Security.Logs.RedactSecrets); err != nil {
		return err
	}
	if value := os.Getenv("SANDBOX_LOG_LEVEL"); value != "" {
		cfg.Observability.LogLevel = value
	}
	if value := os.Getenv("SANDBOX_LOG_FORMAT"); value != "" {
		cfg.Observability.LogFormat = value
	}
	if err := applyBoolEnv("SANDBOX_METRICS_ENABLED", &cfg.Observability.MetricsEnabled); err != nil {
		return err
	}
	if err := applyBoolEnv("SANDBOX_TRACING_ENABLED", &cfg.Observability.TracingEnabled); err != nil {
		return err
	}
	if err := applyBoolEnv("SANDBOX_AUDIT_ENABLED", &cfg.Observability.AuditEnabled); err != nil {
		return err
	}

	return nil
}

func (cfg Config) Validate() error {
	var errs []string
	if cfg.FeatureFlags.LegacyHarness && cfg.App.Environment != "local" && cfg.App.Environment != "dev" && cfg.App.Environment != "test" {
		errs = append(errs, "legacy harness is only available in local/dev/test")
	}
	if cfg.SchemaVersion != ConfigSchemaVersion {
		errs = append(errs, fmt.Sprintf("schema_version must be %q", ConfigSchemaVersion))
	}
	if cfg.App.Name == "" {
		errs = append(errs, "app.name is required")
	}
	if (cfg.App.Environment == "production" || cfg.App.Environment == "prod") && cfg.Storage.Type != "postgres" {
		errs = append(errs, "production requires postgres storage")
	}
	if cfg.App.Environment == "" {
		errs = append(errs, "app.environment is required")
	}
	if cfg.App.WorkerID == "" {
		errs = append(errs, "app.worker_id is required")
	}
	if cfg.App.ShutdownTimeoutMS <= 0 {
		errs = append(errs, "app.shutdown_timeout_ms must be positive")
	}
	if cfg.Server.HealthAddr == "" {
		errs = append(errs, "server.health_addr is required")
	}
	if len(cfg.Kafka.Brokers) == 0 {
		errs = append(errs, "kafka.brokers must not be empty")
	}
	for _, broker := range cfg.Kafka.Brokers {
		if strings.TrimSpace(broker) == "" {
			errs = append(errs, "kafka.brokers must not contain empty entries")
		}
	}
	if err := cfg.Kafka.Security.validateEnvironment(cfg.App.Environment); err != nil {
		errs = append(errs, err.Error())
	}
	if cfg.Scheduler.MaxInFlight <= 0 {
		errs = append(errs, "scheduler.max_in_flight must be positive")
	}
	if cfg.Scheduler.JobTimeoutMS <= 0 {
		errs = append(errs, "scheduler.job_timeout_ms must be positive")
	}
	if cfg.Scheduler.Queue.Policy != "bounded_fifo" {
		errs = append(errs, "scheduler.queue.policy must be bounded_fifo")
	}
	if cfg.Scheduler.Queue.MaxDepth <= 0 {
		errs = append(errs, "scheduler.queue.max_depth must be positive")
	}
	if cfg.Scheduler.Queue.EnqueueTimeoutMS < 0 {
		errs = append(errs, "scheduler.queue.enqueue_timeout_ms must not be negative")
	}
	if cfg.Scheduler.Admission.MaxQueueAgeMS <= 0 {
		errs = append(errs, "scheduler.admission.max_queue_age_ms must be positive")
	}
	if cfg.Executor.ClientTimeoutMS <= 0 {
		errs = append(errs, "executor.client_timeout_ms must be positive")
	}
	if cfg.Kafka.Topics.Requests == "" {
		errs = append(errs, "kafka.topics.requests is required")
	}
	if cfg.Kafka.Topics.Retry == "" {
		errs = append(errs, "kafka.topics.retry is required")
	}
	if cfg.Kafka.Topics.Results == "" {
		errs = append(errs, "kafka.topics.results is required")
	}
	if cfg.Kafka.Topics.DeadLetter == "" {
		errs = append(errs, "kafka.topics.deadletter is required")
	}
	if cfg.Kafka.Retry.MaxAttempts <= 0 {
		errs = append(errs, "kafka.retry.max_attempts must be positive")
	}
	if cfg.Kafka.Retry.InitialBackoffMS < 0 {
		errs = append(errs, "kafka.retry.initial_backoff_ms must not be negative")
	}
	if cfg.Kafka.Retry.MaxBackoffMS < 0 {
		errs = append(errs, "kafka.retry.max_backoff_ms must not be negative")
	}
	if cfg.Kafka.Retry.BackoffMultiplier < 1 {
		errs = append(errs, "kafka.retry.backoff_multiplier must be at least 1")
	}
	if cfg.Kafka.Retry.EarlyRetrySleepCapMS < 0 {
		errs = append(errs, "kafka.retry.early_retry_sleep_cap_ms must not be negative")
	}

	switch cfg.Executor.Type {
	case "fake":
		if !cfg.FeatureFlags.FakeExecutor {
			errs = append(errs, "executor.type=fake requires feature_flags.fake_executor=true")
		}
	case "gojudge":
		if !cfg.FeatureFlags.GoJudgeExecutor {
			errs = append(errs, "executor.type=gojudge requires feature_flags.gojudge_executor=true")
		}
		if cfg.Executor.GoJudge.BaseURL == "" {
			errs = append(errs, "executor.gojudge.base_url is required")
		}
	default:
		errs = append(errs, "executor.type must be fake or gojudge")
	}

	switch cfg.Storage.Type {
	case "memory":
	case "postgres":
		if cfg.Storage.Postgres.DSN == "" {
			errs = append(errs, "storage.postgres.dsn is required when storage.type=postgres")
		}
	default:
		errs = append(errs, "storage.type must be memory or postgres")
	}
	if cfg.Storage.Retention.CompletedRunsDays < 0 {
		errs = append(errs, "storage.retention.completed_runs_days must not be negative")
	}
	if cfg.Storage.Retention.ArtifactsDays < 0 {
		errs = append(errs, "storage.retention.artifacts_days must not be negative")
	}
	if cfg.Storage.Retention.PublishedOutboxDays < 0 {
		errs = append(errs, "storage.retention.published_outbox_days must not be negative")
	}
	if cfg.Storage.Retention.FailedOutboxDays < 0 {
		errs = append(errs, "storage.retention.failed_outbox_days must not be negative")
	}
	if cfg.Storage.Retention.IdempotencyKeysDays < 0 {
		errs = append(errs, "storage.retention.idempotency_keys_days must not be negative")
	}
	if cfg.Storage.Retention.WorkerHeartbeatStaleMS < 0 {
		errs = append(errs, "storage.retention.worker_heartbeat_stale_ms must not be negative")
	}
	if cfg.Storage.Artifacts.Enabled {
		if !cfg.FeatureFlags.ObjectStorageArtifacts {
			errs = append(errs, "storage.artifacts.enabled requires feature_flags.object_storage_artifacts=true")
		}
		switch cfg.Storage.Artifacts.Provider {
		case "filesystem":
			if strings.TrimSpace(cfg.Storage.Artifacts.RootDir) == "" {
				errs = append(errs, "storage.artifacts.root_dir is required when artifact provider is filesystem")
			}
		case "s3":
			if strings.TrimSpace(cfg.Storage.Artifacts.Bucket) == "" {
				errs = append(errs, "storage.artifacts.bucket is required when artifact provider is s3")
			}
			if strings.TrimSpace(cfg.Storage.Artifacts.Region) == "" {
				errs = append(errs, "storage.artifacts.region is required when artifact provider is s3")
			}
			if strings.TrimSpace(cfg.Storage.Artifacts.AccessKeyID) == "" {
				errs = append(errs, "storage.artifacts.access_key_id is required when artifact provider is s3")
			}
			if strings.TrimSpace(cfg.Storage.Artifacts.SecretAccessKey) == "" {
				errs = append(errs, "storage.artifacts.secret_access_key is required when artifact provider is s3")
			}
		default:
			errs = append(errs, "storage.artifacts.provider must be filesystem or s3 when artifacts are enabled")
		}
	} else if cfg.Storage.Artifacts.Provider != "" && cfg.Storage.Artifacts.Provider != "disabled" && cfg.Storage.Artifacts.Provider != "filesystem" && cfg.Storage.Artifacts.Provider != "s3" {
		errs = append(errs, "storage.artifacts.provider must be disabled, filesystem, or s3")
	}
	if cfg.Storage.Artifacts.InlineMaxBytes < 0 {
		errs = append(errs, "storage.artifacts.inline_max_bytes must not be negative")
	}
	if cfg.Storage.Artifacts.MaxBytes < 0 {
		errs = append(errs, "storage.artifacts.max_bytes must not be negative")
	}
	if cfg.Storage.Artifacts.MaxBytes > 0 && cfg.Storage.Artifacts.InlineMaxBytes > cfg.Storage.Artifacts.MaxBytes {
		errs = append(errs, "storage.artifacts.inline_max_bytes must not exceed storage.artifacts.max_bytes")
	}
	if cfg.Storage.Artifacts.RequestTimeoutMS < 0 {
		errs = append(errs, "storage.artifacts.request_timeout_ms must not be negative")
	}

	if cfg.Tests.Inline.Enabled && !cfg.FeatureFlags.InlineTests {
		errs = append(errs, "tests.inline.enabled requires feature_flags.inline_tests=true")
	}
	if cfg.Tests.ObjectStorage.Enabled && cfg.Storage.Artifacts.Enabled && cfg.Tests.ObjectStorage.Provider == "s3" && cfg.Storage.Artifacts.Provider == "s3" && strings.TrimSpace(cfg.Tests.ObjectStorage.AccessKeyID) == strings.TrimSpace(cfg.Storage.Artifacts.AccessKeyID) {
		errs = append(errs, "suite reader and artifact writer credentials must differ")
	}
	if cfg.Tests.ObjectStorage.Enabled {
		if !cfg.FeatureFlags.ObjectStorageTest {
			errs = append(errs, "tests.object_storage.enabled requires feature_flags.object_storage_tests=true")
		}
		if cfg.Tests.ObjectStorage.Provider == "" || cfg.Tests.ObjectStorage.Provider == "disabled" {
			errs = append(errs, "tests.object_storage.provider is required when object storage tests are enabled")
		}
		if cfg.Tests.ObjectStorage.Provider != "s3" {
			errs = append(errs, "tests.object_storage.provider must be s3 when object storage tests are enabled")
		}
		if cfg.Tests.ObjectStorage.Bucket == "" {
			errs = append(errs, "tests.object_storage.bucket is required when object storage tests are enabled")
		}
		if cfg.Tests.ObjectStorage.Region == "" {
			errs = append(errs, "tests.object_storage.region is required when object storage tests are enabled")
		}
		if cfg.Tests.ObjectStorage.AccessKeyID == "" {
			errs = append(errs, "tests.object_storage.access_key_id is required when object storage tests are enabled")
		}
		if cfg.Tests.ObjectStorage.SecretAccessKey == "" {
			errs = append(errs, "tests.object_storage.secret_access_key is required when object storage tests are enabled")
		}
	}
	if cfg.Tests.ObjectStorage.CacheTTLMS < 0 {
		errs = append(errs, "tests.object_storage.cache_ttl_ms must not be negative")
	}
	if cfg.Tests.ObjectStorage.MaxBytes < 0 {
		errs = append(errs, "tests.object_storage.max_bytes must not be negative")
	}
	if cfg.Tests.ObjectStorage.RequestTimeoutMS < 0 {
		errs = append(errs, "tests.object_storage.request_timeout_ms must not be negative")
	}

	if cfg.Observability.LogFormat != "json" && cfg.Observability.LogFormat != "console" {
		errs = append(errs, "observability.log_format must be json or console")
	}
	if !validLogLevel(cfg.Observability.LogLevel) {
		errs = append(errs, "observability.log_level must be debug, info, warn, or error")
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (cfg Config) ShutdownTimeout() time.Duration {
	return time.Duration(cfg.App.ShutdownTimeoutMS) * time.Millisecond
}

func applyBoolEnv(name string, target *bool) error {
	value := os.Getenv(name)
	if value == "" {
		return nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fmt.Errorf("%s must be a boolean: %w", name, err)
	}
	*target = parsed
	return nil
}

func applyIntEnv(name string, target *int) error {
	value := os.Getenv(name)
	if value == "" {
		return nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("%s must be an integer: %w", name, err)
	}
	*target = parsed
	return nil
}

func applyFloatEnv(name string, target *float64) error {
	value := os.Getenv(name)
	if value == "" {
		return nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fmt.Errorf("%s must be a number: %w", name, err)
	}
	*target = parsed
	return nil
}

func applyMapIntEnv(name string, target map[string]int, key string) error {
	value := os.Getenv(name)
	if value == "" {
		return nil
	}
	if target == nil {
		return fmt.Errorf("cannot apply %s because target map is nil", name)
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("%s must be an integer: %w", name, err)
	}
	target[key] = parsed
	return nil
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func validLogLevel(level string) bool {
	switch level {
	case "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}
