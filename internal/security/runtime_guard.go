package security

import "strings"

const CodeRuntimeSecretEnv = "runtime_secret_env"

var sensitiveEnvMarkers = []string{
	"ACCESS_KEY",
	"API_KEY",
	"CREDENTIAL",
	"PASSWORD",
	"PRIVATE_KEY",
	"SECRET",
	"TOKEN",
}

func ValidateRuntimeEnvironment(environment map[string]string) error {
	for name := range environment {
		if IsSensitiveEnvName(name) {
			return violation(CodeRuntimeSecretEnv, "environment."+name, "secrets must not be copied into sandbox runtime")
		}
	}
	return nil
}

func IsSensitiveEnvName(name string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(name))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, ".", "_")
	if normalized == "" {
		return false
	}

	for _, marker := range sensitiveEnvMarkers {
		if normalized == marker || strings.Contains(normalized, "_"+marker) || strings.Contains(normalized, marker+"_") {
			return true
		}
	}
	return false
}
