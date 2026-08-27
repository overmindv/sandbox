package languages_test

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/languages"
	golang "overmindv/sandbox/internal/languages/go"
	"overmindv/sandbox/internal/languages/python"
)

func TestLoadManifestFromConfigFile(t *testing.T) {
	manifest, err := languages.LoadManifest("configs/languages.yaml")
	if err != nil {
		t.Errorf("load manifest: %v", err)
		return
	}

	if manifest.SchemaVersion != languages.ManifestSchemaVersion {
		t.Errorf("expected schema %q, got %q", languages.ManifestSchemaVersion, manifest.SchemaVersion)
	}
	if manifest.Defaults.LanguageID != "python" {
		t.Errorf("expected default language python, got %q", manifest.Defaults.LanguageID)
	}
	if len(manifest.Languages) == 0 {
		t.Error("expected at least one language")
	}
}

func TestRegistryBuildsPythonCommandFromManifest(t *testing.T) {
	registry := testRegistry(t)
	if registry == nil {
		return
	}

	command, err := registry.BuildCommand(context.Background(), validLanguageRunRequest())
	if err != nil {
		t.Errorf("build command: %v", err)
		return
	}

	if command.Adapter != "python" {
		t.Errorf("expected python adapter, got %q", command.Adapter)
	}
	if command.RuntimeImage != "overmindv/sandbox-python:3.12" {
		t.Errorf("expected python runtime image, got %q", command.RuntimeImage)
	}
	if command.WorkingDir != "/workspace" {
		t.Errorf("expected /workspace working dir, got %q", command.WorkingDir)
	}
	if len(command.Argv) == 0 || command.Argv[0] != "python3" {
		t.Errorf("expected python argv, got %#v", command.Argv)
	}
	if command.Environment["PYTHONDONTWRITEBYTECODE"] != "1" {
		t.Errorf("expected Python environment from manifest, got %#v", command.Environment)
	}
	if command.Entrypoint != "solution.py" {
		t.Errorf("expected solution.py entrypoint, got %q", command.Entrypoint)
	}
	if command.TestEntrypoint != "test_solution.py" {
		t.Errorf("expected test_solution.py test entrypoint, got %q", command.TestEntrypoint)
	}
}

func TestRegistryBuildsGoCommandFromManifest(t *testing.T) {
	registry := testRegistry(t)
	if registry == nil {
		return
	}

	req := validLanguageRunRequest()
	req.Language = contracts.LanguageSpec{ID: "go", Version: "1.26"}
	req.Code = contracts.CodeSpec{
		Entrypoint: "solution.go",
		Files: []contracts.SourceFile{
			{
				Path:       "solution.go",
				ContentB64: encodeLanguageFixture("package solution\n\nfunc Add(a, b int) int { return a + b }\n"),
			},
		},
	}
	req.Tests = contracts.TestSpec{
		Mode:       contracts.TestModeGoTest,
		Source:     contracts.TestSourceInline,
		Entrypoint: "solution_test.go",
		Files: []contracts.SourceFile{
			{
				Path:       "solution_test.go",
				ContentB64: encodeLanguageFixture("package solution\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif got := Add(1, 2); got != 3 {\n\t\tt.Errorf(\"Add() = %d\", got)\n\t}\n}\n"),
			},
		},
	}

	command, err := registry.BuildCommand(context.Background(), req)
	if err != nil {
		t.Errorf("build Go command: %v", err)
		return
	}

	if command.Adapter != "go" {
		t.Errorf("expected go adapter, got %q", command.Adapter)
	}
	if command.RuntimeImage != "overmindv/sandbox-go:1.26" {
		t.Errorf("expected Go runtime image, got %q", command.RuntimeImage)
	}
	if command.WorkingDir != "/workspace" {
		t.Errorf("expected /workspace working dir, got %q", command.WorkingDir)
	}
	wantArgv := []string{"/usr/local/go/bin/go", "test", "-json", "-p", "1", "./..."}
	if len(command.Argv) != len(wantArgv) {
		t.Errorf("expected Go argv %#v, got %#v", wantArgv, command.Argv)
		return
	}
	for i := range wantArgv {
		if command.Argv[i] != wantArgv[i] {
			t.Errorf("expected Go argv %#v, got %#v", wantArgv, command.Argv)
			return
		}
	}
	if command.Environment["GOTOOLCHAIN"] != "local" {
		t.Errorf("expected Go environment from manifest, got %#v", command.Environment)
	}
	if command.Environment["GOMAXPROCS"] != "2" {
		t.Errorf("expected Go GOMAXPROCS from manifest, got %#v", command.Environment)
	}
	if command.Environment["PATH"] != "/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin" {
		t.Errorf("expected Go PATH from manifest, got %#v", command.Environment)
	}
	if !containsCommandFile(command.Files, "go.mod", execution.FileKindHarness) {
		t.Errorf("expected generated go.mod harness file, got %#v", command.Files)
	}
}

func TestRegistryRejectsDisabledLanguage(t *testing.T) {
	registry := testRegistry(t)
	if registry == nil {
		return
	}

	req := validLanguageRunRequest()
	req.Language = contracts.LanguageSpec{ID: "javascript", Version: "22"}
	req.Tests.Mode = contracts.TestMode("node")

	_, err := registry.BuildCommand(context.Background(), req)
	if !errors.Is(err, languages.ErrUnsupportedLanguage) {
		t.Errorf("expected unsupported language, got %v", err)
	}
}

func TestRegistryRejectsUnsupportedVersion(t *testing.T) {
	registry := testRegistry(t)
	if registry == nil {
		return
	}

	req := validLanguageRunRequest()
	req.Language.Version = "9.99"

	_, err := registry.BuildCommand(context.Background(), req)
	if !errors.Is(err, languages.ErrUnsupportedLanguage) {
		t.Errorf("expected unsupported language version, got %v", err)
	}
}

func TestRegistryRejectsUnsupportedTestMode(t *testing.T) {
	registry := testRegistry(t)
	if registry == nil {
		return
	}

	req := validLanguageRunRequest()
	req.Tests.Mode = contracts.TestMode("node")

	_, err := registry.BuildCommand(context.Background(), req)
	if !errors.Is(err, languages.ErrUnsupportedLanguage) {
		t.Errorf("expected unsupported test mode, got %v", err)
	}
}

func TestRegistryRejectsDisallowedExtension(t *testing.T) {
	registry := testRegistry(t)
	if registry == nil {
		return
	}

	req := validLanguageRunRequest()
	req.Code.Files[0].Path = "solution.sh"

	_, err := registry.BuildCommand(context.Background(), req)
	if err == nil {
		t.Error("expected disallowed extension error")
	}
}

func TestManifestRejectsSecretRuntimeEnvironment(t *testing.T) {
	manifest := testManifestWithSecretEnvironment(t)

	err := languages.ValidateManifest(manifest)
	if err == nil {
		t.Error("expected secret runtime environment to be rejected")
	}
}

func testRegistry(t *testing.T) *languages.Registry {
	t.Helper()
	manifest, err := languages.LoadManifest("configs/languages.yaml")
	if err != nil {
		t.Errorf("load manifest: %v", err)
		return nil
	}
	registry, err := languages.NewRegistry(manifest, python.NewAdapter(), golang.NewAdapter())
	if err != nil {
		t.Errorf("new registry: %v", err)
		return nil
	}
	return registry
}

func validLanguageRunRequest() contracts.RunRequest {
	return contracts.RunRequest{
		SchemaVersion: contracts.RunRequestSchemaVersion,
		SubmissionID:  "11111111-1111-4111-8111-111111111111",
		AttemptID:     "22222222-2222-4222-8222-222222222222",
		UserID:        "33333333-3333-4333-8333-333333333333",
		TaskID:        "44444444-4444-4444-8444-444444444444",
		Language: contracts.LanguageSpec{
			ID:      "python",
			Version: "3.12",
		},
		Code: contracts.CodeSpec{
			Entrypoint: "solution.py",
			Files: []contracts.SourceFile{
				{
					Path:       "solution.py",
					ContentB64: encodeLanguageFixture("def add(a, b):\n    return a + b\n"),
				},
			},
		},
		Tests: contracts.TestSpec{
			Mode:       contracts.TestModePytest,
			Source:     contracts.TestSourceInline,
			Entrypoint: "test_solution.py",
			Files: []contracts.SourceFile{
				{
					Path:       "test_solution.py",
					ContentB64: encodeLanguageFixture("from solution import add\n"),
				},
			},
		},
		Limits: contracts.Limits{
			CPUms:          2000,
			Wallms:         5000,
			MemoryMB:       128,
			PIDs:           32,
			StdoutBytes:    32768,
			StderrBytes:    32768,
			WorkspaceBytes: 1048576,
		},
		TraceID:   "trace-language",
		CreatedAt: time.Now().UTC(),
	}
}

func testManifestWithSecretEnvironment(t *testing.T) config.LanguageRegistryConfig {
	t.Helper()
	manifest, err := languages.LoadManifest("configs/languages.yaml")
	if err != nil {
		t.Errorf("load manifest: %v", err)
		return config.LanguageRegistryConfig{}
	}
	if len(manifest.Languages) == 0 || len(manifest.Languages[0].Versions) == 0 {
		return manifest
	}
	if manifest.Languages[0].Versions[0].Execution.Environment == nil {
		manifest.Languages[0].Versions[0].Execution.Environment = map[string]string{}
	}
	manifest.Languages[0].Versions[0].Execution.Environment["AWS_SECRET_ACCESS_KEY"] = "leak"
	return manifest
}

func encodeLanguageFixture(value string) string {
	return base64.StdEncoding.EncodeToString([]byte(value))
}

func containsCommandFile(files []execution.ExecutionFile, path string, kind execution.FileKind) bool {
	for _, file := range files {
		if file.Path == path && file.Kind == kind {
			return true
		}
	}
	return false
}
