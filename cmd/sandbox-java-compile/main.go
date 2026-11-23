package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"overmindv/sandbox/internal/javaclass"
)

func main() {
	os.Exit(compile(os.Args[1:], os.Stderr))
}

func compile(args []string, stderr io.Writer) int {
	if len(args) < 2 || len(args)-1 > javaclass.MaxSources {
		return 2
	}
	main, ok := javaclass.MainClass(args[0])
	if !ok {
		return 2
	}
	classes, err := os.MkdirTemp(".", "_sandbox_javac_")
	if err != nil {
		return 2
	}
	javac := []string{
		"-J-Xmx256m", "-J-XX:+UseSerialGC", "-J-XX:ActiveProcessorCount=1",
		"-J-XX:-UseContainerSupport", "-J-XX:-UsePerfData", "-J-XX:-CreateCoredumpOnCrash",
		"-J-XX:ErrorFile=/dev/null", "--release", "21", "-encoding", "UTF-8",
		"-proc:none", "-implicit:none", "-Xmaxerrs", "30",
		"-classpath", classes, "-sourcepath", classes, "-d", classes,
	}
	for _, name := range args[1:] {
		if _, ok := javaclass.MainClass(name); !ok {
			return 2
		}
		javac = append(javac, "./"+name)
	}
	// This tool runs inside go-judge with the compiler's resource limits.
	cmd := exec.Command("/usr/bin/javac", javac...)
	cmd.Stderr = stderr
	cmd.Env = []string{"LC_ALL=C.UTF-8", "PATH=/usr/bin:/bin"}
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return javaclass.CompileErrorExit
		}
		return 2
	}
	classFile := filepath.FromSlash(args[0][:len(args[0])-len(".java")] + ".class")
	if _, err := os.Stat(filepath.Join(classes, classFile)); errors.Is(err, os.ErrNotExist) {
		_, _ = fmt.Fprintln(stderr, "Java entrypoint class was not generated")
		return javaclass.CompileErrorExit
	} else if err != nil {
		return 2
	}
	var archive bytes.Buffer
	if err := javaclass.WriteArchive(&archive, os.DirFS(classes)); err != nil {
		if errors.Is(err, javaclass.ErrLimit) {
			return javaclass.ArtifactLimitExit
		}
		if errors.Is(err, javaclass.ErrInvalid) {
			_, _ = fmt.Fprintln(stderr, "Java class names must use ASCII identifiers")
			return javaclass.CompileErrorExit
		}
		return 2
	}
	if javaclass.ValidateArchive(archive.Bytes(), main) != nil || os.WriteFile(javaclass.ArchiveName, archive.Bytes(), 0o600) != nil {
		return 2
	}
	return 0
}
