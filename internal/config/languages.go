package config

type LanguageRegistryConfig struct {
	SchemaVersion string           `yaml:"schema_version"`
	Defaults      LanguageDefaults `yaml:"defaults"`
	Languages     []LanguageConfig `yaml:"languages"`
}

type LanguageDefaults struct {
	LanguageID    string `yaml:"language_id"`
	Version       string `yaml:"version"`
	TestMode      string `yaml:"test_mode"`
	LimitsProfile string `yaml:"limits_profile"`
}

type LanguageConfig struct {
	ID          string                  `yaml:"id"`
	DisplayName string                  `yaml:"display_name"`
	Enabled     bool                    `yaml:"enabled"`
	Adapter     string                  `yaml:"adapter"`
	Versions    []LanguageVersionConfig `yaml:"versions"`
}

type LanguageVersionConfig struct {
	Version        string                  `yaml:"version"`
	Default        bool                    `yaml:"default"`
	RuntimeImage   string                  `yaml:"runtime_image"`
	RuntimeVersion string                  `yaml:"runtime_version,omitempty"`
	LimitsProfile  string                  `yaml:"limits_profile"`
	TestModes      []string                `yaml:"test_modes"`
	FilePolicy     LanguageFilePolicy      `yaml:"file_policy"`
	Execution      LanguageExecutionConfig `yaml:"execution"`
}

type LanguageFilePolicy struct {
	MaxFiles              int      `yaml:"max_files"`
	AllowedExtensions     []string `yaml:"allowed_extensions"`
	DefaultEntrypoint     string   `yaml:"default_entrypoint"`
	DefaultTestEntrypoint string   `yaml:"default_test_entrypoint"`
}

type LanguageExecutionConfig struct {
	Kind        string            `yaml:"kind"`
	WorkingDir  string            `yaml:"working_dir"`
	Compile     *CommandConfig    `yaml:"compile"`
	Command     CommandConfig     `yaml:"command"`
	Environment map[string]string `yaml:"environment"`
}

type CommandConfig struct {
	Argv []string `yaml:"argv"`
}
