package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"overmindv/sandbox/internal/javaclass"
)

func main() {
	os.Exit(compile(os.Args[1:], os.Stderr))
}

func compile(args []string, stderr io.Writer) int {
	if len(args) < 2 || len(args)-1 > javaclass.MaxSources {
		return 2
	}
	main, ok := javaclass.KotlinMainClass(args[0])
	if !ok {
		return 2
	}
	classes, err := os.MkdirTemp(".", "_sandbox_kotlinc_")
	if err != nil {
		return 2
	}
	argv := []string{
		"-Xmx384m", "-XX:+UseSerialGC", "-XX:ActiveProcessorCount=2",
		"-XX:TieredStopAtLevel=1", "-XX:-UseContainerSupport", "-XX:-UsePerfData",
		"-XX:+DisableAttachMechanism", "-XX:-CreateCoredumpOnCrash", "-XX:ErrorFile=/dev/null",
		"-jar", "/usr/local/share/sandbox/kotlin-compiler.jar",
		"-Xjdk-release=21", "-Xdisable-default-scripting-plugin", "-no-stdlib", "-no-reflect",
		"-classpath", javaclass.KotlinStdlibPath, "-module-name", "sandbox", "-d", classes,
	}
	for _, source := range args[1:] {
		if _, ok := javaclass.KotlinMainClass(source); !ok {
			return 2
		}
		argv = append(argv, "./"+source)
	}
	cmd := exec.Command("/usr/bin/java", argv...)
	cmd.Stderr = stderr
	cmd.Env = []string{"LC_ALL=C.UTF-8", "PATH=/usr/bin:/bin"}
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return javaclass.CompileErrorExit
		}
		return 2
	}
	classFile := filepath.FromSlash(strings.ReplaceAll(main, ".", "/") + ".class")
	if _, err := os.Stat(filepath.Join(classes, classFile)); errors.Is(err, os.ErrNotExist) {
		_, _ = fmt.Fprintln(stderr, "Kotlin entrypoint class was not generated")
		return javaclass.CompileErrorExit
	} else if err != nil {
		return 2
	}
	var archive bytes.Buffer
	if err := javaclass.WriteKotlinArchive(&archive, os.DirFS(classes)); err != nil {
		switch {
		case errors.Is(err, javaclass.ErrLimit):
			return javaclass.ArtifactLimitExit
		case errors.Is(err, javaclass.ErrInvalid):
			_, _ = fmt.Fprintln(stderr, "Kotlin class names must use ASCII identifiers")
			return javaclass.CompileErrorExit
		default:
			return 2
		}
	}
	if javaclass.ValidateArchive(archive.Bytes(), main) != nil ||
		os.WriteFile(javaclass.KotlinArchiveName, archive.Bytes(), 0o600) != nil {
		return 2
	}
	return 0
}
