package main

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"overmindv/sandbox/internal/gotestjob"
)

func TestBuildTestsKeepsPackagesWithTheSameBasenameSeparate(t *testing.T) {
	workspace := t.TempDir()
	writeFixture(t, workspace, "go.mod", "module example.com/submission\n\ngo 1.26\n")
	writeFixture(t, workspace, "a/calc/solution.go", "package calc\n")
	writeFixture(t, workspace, "a/calc/solution_test.go", `package calc
import "testing"
func TestAnswer(t *testing.T) { panic("compiler must not run tests") }`)
	writeFixture(t, workspace, "b/calc/solution_test.go", `package calc_test
import "testing"
func TestAnswer(t *testing.T) {}`)
	writeFixture(t, workspace, "b/calc/solution.go", "package calc\n")
	writeFixture(t, workspace, "helpers/solution.go", "package helpers\n")

	var bundle, logs bytes.Buffer
	if err := buildTests(workspace, testGo(t), &bundle, &logs, &logs); err != nil {
		t.Error(err, logs.String())
		return
	}

	reader := tar.NewReader(&bundle)
	var names []string
	var packages []gotestjob.Package
	for {
		entry, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Error(err)
			return
		}
		names = append(names, entry.Name)
		if entry.Name == gotestjob.IndexName {
			if err := json.NewDecoder(reader).Decode(&packages); err != nil {
				t.Error(err)
			}
		} else if entry.Size <= 0 || entry.Size > gotestjob.MaxBinaryBytes {
			t.Error("unexpected binary size", entry.Size)
		}
	}
	if !slices.Equal(names, []string{gotestjob.BinaryName(0), gotestjob.BinaryName(1), gotestjob.IndexName}) {
		t.Error("bundle entries", names)
	}
	if !slices.Equal(packages, []gotestjob.Package{
		{Dir: "a/calc", ImportPath: "example.com/submission/a/calc"},
		{Dir: "b/calc", ImportPath: "example.com/submission/b/calc"},
	}) {
		t.Error("package index", packages)
	}
}

func TestBuildTestsChecksPackagesWithoutTests(t *testing.T) {
	workspace := t.TempDir()
	writeFixture(t, workspace, "go.mod", "module example.com/submission\n\ngo 1.26\n")
	writeFixture(t, workspace, "solution.go", "package solution\nvar result = missing\n")
	var bundle, logs bytes.Buffer
	err := buildTests(workspace, testGo(t), &bundle, &logs, &logs)
	if !errors.Is(err, errBuild) || !bytes.Contains(logs.Bytes(), []byte("undefined: missing")) {
		t.Error("unexpected compiler failure", err, logs.String())
	}
}

func TestPackageDirRejectsPathsOutsideWorkspace(t *testing.T) {
	parent := t.TempDir()
	if _, err := packageDir(filepath.Join(parent, "workspace"), filepath.Join(parent, "outside")); !errors.Is(err, errBuild) {
		t.Error("unexpected path result", err)
	}
}

func testGo(t *testing.T) string {
	t.Helper()
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOPROXY", "off")
	t.Setenv("CGO_ENABLED", "0")
	tool, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

func writeFixture(t *testing.T, workspace, name, content string) {
	t.Helper()
	path := filepath.Join(workspace, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
