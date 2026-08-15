package security

import (
	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
)

const (
	CodePathTraversal      = "path_traversal"
	CodeAbsolutePath       = "absolute_path"
	CodeUnsafeFilename     = "unsafe_filename"
	CodeTooManyFiles       = "too_many_files"
	CodeFileTooLarge       = "file_too_large"
	CodePayloadTooLarge    = "payload_too_large"
	CodeArchiveDisabled    = "archive_disabled"
	CodeCopyOutSymlink     = "copy_out_symlink"
	CodeInvalidFileContent = "invalid_file_content"
)

type Policy struct {
	MaxPayloadBytes int
	MaxFileBytes    int
	MaxSourceFiles  int
	MaxTestFiles    int
	MaxFiles        int
	MaxPathLength   int

	AllowAbsolutePaths  bool
	AllowParentSegments bool
	AllowArchives       bool
	AllowSymlinks       bool

	MaxStdoutBytes int
	MaxStderrBytes int
	TruncateOutput bool
}

func DefaultPolicy() Policy {
	return Policy{
		MaxPayloadBytes:     2 * 1024 * 1024,
		MaxFileBytes:        1024 * 1024,
		MaxSourceFiles:      32,
		MaxTestFiles:        32,
		MaxFiles:            64,
		MaxPathLength:       128,
		AllowAbsolutePaths:  false,
		AllowParentSegments: false,
		AllowArchives:       false,
		AllowSymlinks:       false,
		MaxStdoutBytes:      32768,
		MaxStderrBytes:      32768,
		TruncateOutput:      true,
	}
}

func PolicyFromConfig(cfg config.Config) Policy {
	policy := DefaultPolicy()
	if cfg.Security.Filesystem.MaxFiles > 0 {
		policy.MaxFiles = cfg.Security.Filesystem.MaxFiles
	}
	if cfg.Tests.Inline.MaxFiles > 0 {
		policy.MaxTestFiles = cfg.Tests.Inline.MaxFiles
	}
	if cfg.Security.Logs.MaxStdoutBytes >= 0 {
		policy.MaxStdoutBytes = cfg.Security.Logs.MaxStdoutBytes
	}
	if cfg.Security.Logs.MaxStderrBytes >= 0 {
		policy.MaxStderrBytes = cfg.Security.Logs.MaxStderrBytes
	}

	policy.AllowAbsolutePaths = cfg.Security.Filesystem.AllowAbsolutePaths
	policy.AllowParentSegments = cfg.Security.Filesystem.AllowParentSegments
	policy.AllowSymlinks = cfg.Security.Filesystem.AllowSymlinks
	return policy.withDefaults()
}

func PolicyFromConfigAndLimits(cfg config.Config, limits config.LimitsRegistryConfig) Policy {
	policy := PolicyFromConfig(cfg)
	if limits.Global.RequestMaxBytes > 0 {
		policy.MaxPayloadBytes = limits.Global.RequestMaxBytes
	}
	if limits.Global.FileContentMaxBytes > 0 {
		policy.MaxFileBytes = limits.Global.FileContentMaxBytes
	}
	if limits.Global.SourceFilesMaxCount > 0 {
		policy.MaxSourceFiles = limits.Global.SourceFilesMaxCount
	}
	if limits.Global.TestFilesMaxCount > 0 {
		policy.MaxTestFiles = limits.Global.TestFilesMaxCount
	}
	policy.TruncateOutput = limits.Policy.TruncateOutputOnLimit
	return policy.withDefaults()
}

func ValidationOptionsFromLimits(limits config.LimitsRegistryConfig) contracts.ValidationOptions {
	options := contracts.DefaultValidationOptions()
	if limits.Global.SourceFilesMaxCount > 0 {
		options.MaxSourceFiles = limits.Global.SourceFilesMaxCount
	}
	if limits.Global.TestFilesMaxCount > 0 {
		options.MaxTestFiles = limits.Global.TestFilesMaxCount
	}
	if limits.Global.FileContentMaxBytes > 0 {
		options.MaxFileContentByte = limits.Global.FileContentMaxBytes
	}
	if limits.Global.MetadataMaxProperties > 0 {
		options.MaxMetadataKeys = limits.Global.MetadataMaxProperties
	}
	if limits.HardMax.CPUms > 0 {
		options.HardMaxLimits.CPUms = limits.HardMax.CPUms
	}
	if limits.HardMax.WallMS > 0 {
		options.HardMaxLimits.Wallms = limits.HardMax.WallMS
	}
	if limits.HardMax.MemoryMB > 0 {
		options.HardMaxLimits.MemoryMB = limits.HardMax.MemoryMB
	}
	if limits.HardMax.PIDs > 0 {
		options.HardMaxLimits.PIDs = limits.HardMax.PIDs
	}
	if limits.HardMax.StdoutBytes > 0 {
		options.HardMaxLimits.StdoutBytes = limits.HardMax.StdoutBytes
	}
	if limits.HardMax.StderrBytes > 0 {
		options.HardMaxLimits.StderrBytes = limits.HardMax.StderrBytes
	}
	if limits.HardMax.WorkspaceBytes > 0 {
		options.HardMaxLimits.WorkspaceBytes = limits.HardMax.WorkspaceBytes
	}
	return options
}

func (p Policy) withDefaults() Policy {
	defaults := DefaultPolicy()
	if p == (Policy{}) {
		return defaults
	}
	if p.MaxPayloadBytes <= 0 {
		p.MaxPayloadBytes = defaults.MaxPayloadBytes
	}
	if p.MaxFileBytes <= 0 {
		p.MaxFileBytes = defaults.MaxFileBytes
	}
	if p.MaxSourceFiles <= 0 {
		p.MaxSourceFiles = defaults.MaxSourceFiles
	}
	if p.MaxTestFiles <= 0 {
		p.MaxTestFiles = defaults.MaxTestFiles
	}
	if p.MaxFiles <= 0 {
		p.MaxFiles = defaults.MaxFiles
	}
	if p.MaxPathLength <= 0 {
		p.MaxPathLength = defaults.MaxPathLength
	}
	if p.MaxStdoutBytes < 0 {
		p.MaxStdoutBytes = defaults.MaxStdoutBytes
	}
	if p.MaxStderrBytes < 0 {
		p.MaxStderrBytes = defaults.MaxStderrBytes
	}
	return p
}

type ViolationError struct {
	Code    string
	Field   string
	Message string
}

func (e ViolationError) Error() string {
	if e.Field == "" {
		return e.Code + ": " + e.Message
	}
	return e.Code + ": " + e.Field + ": " + e.Message
}

func violation(code string, field string, message string) ViolationError {
	return ViolationError{Code: code, Field: field, Message: message}
}
