package gojudge

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"overmindv/sandbox/internal/gotestjob"
	"overmindv/sandbox/internal/security"
)

type goTestPackage struct {
	gotestjob.Package
	binary []byte
}

// Read entries as slices of the bounded artifact, without extracting any files.
func readGoTestBundle(data []byte) ([]goTestPackage, error) {
	var end [1024]byte
	if len(data) < len(end) || len(data) > gotestjob.MaxBundleBytes ||
		!bytes.Equal(data[len(data)-len(end):], end[:]) {
		return nil, ErrProtocol
	}
	source := bytes.NewReader(data)
	reader := tar.NewReader(source)
	var binaries [][]byte
	for {
		entry, err := reader.Next()
		if err != nil || entry.Typeflag != tar.TypeReg || entry.Format != tar.FormatUSTAR || entry.Size <= 0 || entry.Size > int64(source.Len()) {
			return nil, ErrProtocol
		}
		start := len(data) - source.Len()
		content := data[start : start+int(entry.Size)]
		if entry.Name == gotestjob.IndexName {
			if entry.Size > gotestjob.MaxIndexBytes {
				return nil, ErrProtocol
			}
			packages, err := readGoTestIndex(content, binaries)
			if err != nil {
				return nil, err
			}
			_, err = reader.Next()
			if !errors.Is(err, io.EOF) || source.Len() != 0 {
				return nil, ErrProtocol
			}
			return packages, nil
		}
		if len(binaries) >= gotestjob.MaxPackages || entry.Name != gotestjob.BinaryName(len(binaries)) ||
			entry.Size > gotestjob.MaxBinaryBytes || !isELF(content) {
			return nil, ErrProtocol
		}
		binaries = append(binaries, content)
	}
}

func readGoTestIndex(content []byte, binaries [][]byte) ([]goTestPackage, error) {
	var index []gotestjob.Package
	if json.Unmarshal(content, &index) != nil || index == nil || len(index) != len(binaries) {
		return nil, ErrProtocol
	}
	packages := make([]goTestPackage, 0, len(index))
	dirs := make(map[string]struct{}, len(index))
	names := make(map[string]struct{}, len(index))
	policy := security.DefaultPolicy()
	for i, item := range index {
		if item.Dir != "." {
			if path, err := security.NormalizeRelativePath(item.Dir, policy); err != nil || path != item.Dir {
				return nil, ErrProtocol
			}
		}
		if len(item.ImportPath) == 0 || !utf8.ValidString(item.ImportPath) || strings.ContainsFunc(item.ImportPath, invalidImportRune) {
			return nil, ErrProtocol
		}
		if _, exists := dirs[item.Dir]; exists {
			return nil, ErrProtocol
		}
		if _, exists := names[item.ImportPath]; exists {
			return nil, ErrProtocol
		}
		dirs[item.Dir] = struct{}{}
		names[item.ImportPath] = struct{}{}
		packages = append(packages, goTestPackage{Package: item, binary: binaries[i]})
	}
	return packages, nil
}

func invalidImportRune(r rune) bool {
	return unicode.IsControl(r) || unicode.IsSpace(r)
}
