package security

import "strings"

func TruncateOutput(stdout string, stderr string, policy Policy) (string, string, bool) {
	policy = policy.withDefaults()
	if !policy.TruncateOutput {
		return stdout, stderr, false
	}

	nextStdout, stdoutTruncated := truncateStringBytes(stdout, policy.MaxStdoutBytes)
	nextStderr, stderrTruncated := truncateStringBytes(stderr, policy.MaxStderrBytes)
	return nextStdout, nextStderr, stdoutTruncated || stderrTruncated
}

func truncateStringBytes(value string, maxBytes int) (string, bool) {
	if maxBytes < 0 {
		return value, false
	}
	if len(value) <= maxBytes {
		return value, false
	}
	if maxBytes == 0 {
		return "", value != ""
	}
	return strings.ToValidUTF8(value[:maxBytes], ""), true
}
