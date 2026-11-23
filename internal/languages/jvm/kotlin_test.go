package jvm

import (
	"errors"
	"slices"
	"testing"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/javaclass"
	"overmindv/sandbox/internal/languages"
)

func TestKotlinCommand(t *testing.T) {
	t.Parallel()
	manifest, err := languages.LoadManifest("configs/languages.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var resolved languages.ResolvedLanguage
	for _, entry := range manifest.Languages {
		if entry.ID == KotlinName {
			if entry.Enabled || len(entry.Versions) != 1 || entry.Versions[0].Version != "2.4" {
				t.Fatal("unexpected Kotlin profile", entry)
			}
			resolved = languages.ResolvedLanguage{Language: entry, Version: entry.Versions[0]}
		}
	}
	req, _ := javaRequest()
	req.Code.Files = []contracts.SourceFile{{Path: "Main.kt"}, {Path: "pkg/Helper.kt"}}
	command, err := Kotlin().BuildCommand(t.Context(), req, resolved)
	if err != nil {
		t.Fatal(err)
	}
	wantCompile := []string{javaclass.KotlinCompilerPath, "Main.kt", "Main.kt", "pkg/Helper.kt"}
	wantRun := []string{"-cp", javaclass.KotlinStdlibPath + ":./" + javaclass.KotlinArchiveName, "MainKt"}
	if !slices.Equal(command.CompileArgv, wantCompile) || !slices.Equal(command.Argv[len(command.Argv)-3:], wantRun) ||
		command.Argv[0] != "/usr/bin/java" || !slices.Contains(command.Argv, "-Xmx64m") || command.Adapter != KotlinName {
		t.Error("unexpected Kotlin command", command.CompileArgv, command.Argv)
	}
}

func TestKotlinRejectsUnsupportedSource(t *testing.T) {
	t.Parallel()
	req, resolved := javaRequest()
	req.Code.Entrypoint = "pkg/main.kt"
	for _, filename := range []string{"@Other.kt", "-Other.kt", "Other.kts", "other.jar", "Other.java", "pkg/../Other.kt"} {
		t.Run(filename, func(t *testing.T) {
			req.Code.Files = []contracts.SourceFile{{Path: req.Code.Entrypoint}, {Path: filename}}
			if _, err := Kotlin().BuildCommand(t.Context(), req, resolved); !errors.Is(err, execution.ErrInvalidCommandPayload) {
				t.Error("invalid Kotlin source accepted", err)
			}
		})
	}
	main, ok := javaclass.KotlinMainClass("pkg/main.kt")
	if !ok || main != "pkg.MainKt" {
		t.Error("unexpected package part name", main)
	}
}
