package security

import (
	"bytes"
	"path"
	"strings"
)

var archiveExtensions = []string{
	".zip",
	".tar",
	".tgz",
	".gz",
	".bz2",
	".xz",
	".7z",
	".rar",
	".jar",
	".war",
	".whl",
}

func ValidateArchiveDisabled(filePath string, content []byte, policy Policy) error {
	policy = policy.withDefaults()
	if policy.AllowArchives {
		return nil
	}
	if LooksLikeArchive(filePath, content) {
		return violation(CodeArchiveDisabled, filePath, "archives are disabled")
	}
	return nil
}

func LooksLikeArchive(filePath string, content []byte) bool {
	name := strings.ToLower(path.Base(filePath))
	for _, ext := range archiveExtensions {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	if strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tar.xz") || strings.HasSuffix(name, ".tar.bz2") {
		return true
	}

	switch {
	case bytes.HasPrefix(content, []byte{'P', 'K', 0x03, 0x04}):
		return true
	case bytes.HasPrefix(content, []byte{'P', 'K', 0x05, 0x06}):
		return true
	case bytes.HasPrefix(content, []byte{0x1f, 0x8b}):
		return true
	case bytes.HasPrefix(content, []byte{'B', 'Z', 'h'}):
		return true
	case bytes.HasPrefix(content, []byte{0xfd, '7', 'z', 'X', 'Z', 0x00}):
		return true
	case bytes.HasPrefix(content, []byte{'R', 'a', 'r', '!', 0x1a, 0x07}):
		return true
	case bytes.HasPrefix(content, []byte{'7', 'z', 0xbc, 0xaf, 0x27, 0x1c}):
		return true
	case len(content) > 265 && string(content[257:262]) == "ustar":
		return true
	default:
		return false
	}
}
