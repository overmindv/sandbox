package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"overmindv/sandbox/internal/gotestjob"
)

func main() {
	err := run()
	fmt.Fprintln(os.Stderr, "Go test runner:", err)
	os.Exit(125)
}

func run() error {
	if len(os.Args) != 3 {
		return fmt.Errorf("expected a package directory and import path")
	}
	binary, err := filepath.Abs(gotestjob.RunBinaryName)
	if err != nil {
		return err
	}
	if err := os.Chdir(os.Args[1]); err != nil {
		return err
	}
	const converter = "/usr/local/bin/sandbox-go-test2json"
	return syscall.Exec(converter, []string{
		converter, "-p", os.Args[2], "-t", binary, "-test.v=test2json", "-test.paniconexit0",
	}, os.Environ())
}
