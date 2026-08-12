package python

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
)

const (
	DefaultEntrypoint     = "solution.py"
	DefaultTestEntrypoint = "test_solution.py"
	HarnessPluginPath     = "_sandbox_pytest_plugin.py"
	ReportPath            = ".sandbox/pytest-report.json"
	reportDir             = ".sandbox"
)

var ErrReservedWorkspacePath = errors.New("reserved python workspace path")

func buildWorkspaceFiles(files []execution.ExecutionFile) []execution.ExecutionFile {
	files = append([]execution.ExecutionFile(nil), files...)
	files = append(files, HarnessFile())
	return files
}

func rejectReservedPaths(files []contracts.SourceFile) error {
	for _, file := range files {
		normalized := strings.TrimSpace(filepath.ToSlash(filepath.Clean(file.Path)))
		normalized = strings.TrimPrefix(normalized, "./")
		normalizedLower := strings.ToLower(normalized)

		switch {
		case normalizedLower == strings.ToLower(HarnessPluginPath):
			return fmt.Errorf("%w: %s", ErrReservedWorkspacePath, file.Path)
		case normalizedLower == strings.ToLower(ReportPath):
			return fmt.Errorf("%w: %s", ErrReservedWorkspacePath, file.Path)
		case normalizedLower == reportDir || strings.HasPrefix(normalizedLower, reportDir+"/"):
			return fmt.Errorf("%w: %s", ErrReservedWorkspacePath, file.Path)
		}
	}
	return nil
}
