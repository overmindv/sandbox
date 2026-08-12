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
	FakeExecutor      bool `yaml:"fake_executor"`
	GoJudgeExecutor   bool `yaml:"gojudge_executor"`
	InlineTests       bool `yaml:"inline_tests"`
	ObjectStorageTest bool `yaml:"object_storage_tests"`
}

type KafkaConfig struct {
	Brokers  []string            `yaml:"brokers"`
	ClientID string              `yaml:"client_id"`
	GroupID  string              `yaml:"group_id"`
	Topics   KafkaTopics         `yaml:"topics"`
	Consumer KafkaConsumerConfig `yaml:"consumer"`
	Producer KafkaProducerConfig `yaml:"producer"`
}

type KafkaTopics struct {
	Requests   string `yaml:"requests"`
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
	Enabled    bool   `yaml:"enabled"`
	Provider   string `yaml:"provider"`
	Bucket     string `yaml:"bucket"`
	Endpoint   string `yaml:"endpoint"`
	Region     string `yaml:"region"`
	CacheTTLMS int    `yaml:"cache_ttl_ms"`
}

type StorageConfig struct {
	Type     string        `yaml:"type"`
	Postgres PostgresStore `yaml:"postgres"`
}

type PostgresStore struct {
	DSN                string `yaml:"dsn"`
	MaxOpenConnections int    `yaml:"max_open_connections"`
	MaxIdleConnections int    `yaml:"max_idle_connections"`
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
			FakeExecutor:      true,
			GoJudgeExecutor:   false,
			InlineTests:       true,
			ObjectStorageTest: false,
		},
		Kafka: KafkaConfig{
			Brokers:  []string{"localhost:9092"},
			ClientID: "sandbox-worker-local",
			GroupID:  "sandbox-worker-local",
			Topics: KafkaTopics{
				Requests:   "sandbox.run.requests.v1",
				Events:     "sandbox.run.events.v1",
				Results:    "sandbox.run.results.v1",
				DeadLetter: "sandbox.run.deadletter.v1",
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
				Provider: "disabled",
			},
		},
		Storage: StorageConfig{
			Type: "memory",
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
	if value := os.Getenv("SANDBOX_KAFKA_TOPIC_EVENTS"); value != "" {
		cfg.Kafka.Topics.Events = value
	}
	if value := os.Getenv("SANDBOX_KAFKA_TOPIC_RESULTS"); value != "" {
		cfg.Kafka.Topics.Results = value
	}
	if value := os.Getenv("SANDBOX_KAFKA_TOPIC_DEADLETTER"); value != "" {
		cfg.Kafka.Topics.DeadLetter = value
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
	if err := applyBoolEnv("SANDBOX_FEATURE_INLINE_TESTS", &cfg.FeatureFlags.InlineTests); err != nil {
		return err
	}
	if err := applyBoolEnv("SANDBOX_FEATURE_OBJECT_STORAGE_TESTS", &cfg.FeatureFlags.ObjectStorageTest); err != nil {
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
	if value := os.Getenv("SANDBOX_STORAGE_TYPE"); value != "" {
		cfg.Storage.Type = value
	}
	if value := os.Getenv("SANDBOX_POSTGRES_DSN"); value != "" {
		cfg.Storage.Postgres.DSN = value
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
	if cfg.SchemaVersion != ConfigSchemaVersion {
		errs = append(errs, fmt.Sprintf("schema_version must be %q", ConfigSchemaVersion))
	}
	if cfg.App.Name == "" {
		errs = append(errs, "app.name is required")
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

	if cfg.Tests.Inline.Enabled && !cfg.FeatureFlags.InlineTests {
		errs = append(errs, "tests.inline.enabled requires feature_flags.inline_tests=true")
	}
	if cfg.Tests.ObjectStorage.Enabled {
		if !cfg.FeatureFlags.ObjectStorageTest {
			errs = append(errs, "tests.object_storage.enabled requires feature_flags.object_storage_tests=true")
		}
		if cfg.Tests.ObjectStorage.Provider == "" || cfg.Tests.ObjectStorage.Provider == "disabled" {
			errs = append(errs, "tests.object_storage.provider is required when object storage tests are enabled")
		}
		if cfg.Tests.ObjectStorage.Bucket == "" {
			errs = append(errs, "tests.object_storage.bucket is required when object storage tests are enabled")
		}
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
