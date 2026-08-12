package config

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
