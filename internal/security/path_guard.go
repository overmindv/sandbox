package security

import (
	"path"
	"regexp"
	"strings"
	"unicode/utf8"
)

var safeFilenameSegment = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func NormalizeRelativePath(value string, policy Policy) (string, error) {
	policy = policy.withDefaults()
	if value == "" {
		return "", violation(CodeUnsafeFilename, "path", "must not be empty")
	}
	if len(value) > policy.MaxPathLength {
		return "", violation(CodeUnsafeFilename, "path", "path is too long")
	}
	if !utf8.ValidString(value) {
		return "", violation(CodeUnsafeFilename, "path", "path must be valid utf-8")
	}
	if strings.TrimSpace(value) != value {
		return "", violation(CodeUnsafeFilename, "path", "path must not contain surrounding whitespace")
	}
	if strings.Contains(value, "\\") {
		return "", violation(CodeUnsafeFilename, "path", "path must use forward slashes")
	}
	if strings.Contains(value, "\x00") || containsControl(value) {
		return "", violation(CodeUnsafeFilename, "path", "path must not contain control characters")
	}
	if strings.Contains(value, "//") {
		return "", violation(CodeUnsafeFilename, "path", "path must not contain empty segments")
	}
	if isAbsolutePath(value) && !policy.AllowAbsolutePaths {
		return "", violation(CodeAbsolutePath, "path", "absolute paths are not allowed")
	}

	segments := strings.Split(value, "/")
	for _, segment := range segments {
		switch segment {
		case "", ".":
			return "", violation(CodeUnsafeFilename, "path", "path must not contain empty or current segments")
		case "..":
			if !policy.AllowParentSegments {
				return "", violation(CodePathTraversal, "path", "parent path segments are not allowed")
			}
		}
		if !safeFilenameSegment.MatchString(segment) {
			return "", violation(CodeUnsafeFilename, "path", "filename contains unsupported characters")
		}
	}

	normalized := path.Clean(value)
	if normalized == "." || normalized == ".." || strings.HasPrefix(normalized, "../") {
		return "", violation(CodePathTraversal, "path", "path escapes the workspace")
	}
	return normalized, nil
}

func ValidateWorkspacePath(value string, policy Policy) error {
	_, err := NormalizeRelativePath(value, policy)
	return err
}

func ValidateCopyOutPath(value string, policy Policy) (string, error) {
	normalized, err := NormalizeRelativePath(value, policy)
	if err != nil {
		return "", err
	}
	return normalized, nil
}

func isAbsolutePath(value string) bool {
	if strings.HasPrefix(value, "/") {
		return true
	}
	if len(value) >= 2 && value[1] == ':' {
		return true
	}
	if strings.HasPrefix(value, "//") {
		return true
	}
	return false
}

func containsControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
