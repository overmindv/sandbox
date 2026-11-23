package tsjob

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"io/fs"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	CompilerPath   = "/usr/local/bin/sandbox-ts-compile"
	RunnerPath     = "/usr/local/bin/sandbox-ts-run"
	NodePath       = "/usr/local/bin/node"
	BundleName     = "_sandbox_ts.tar"
	MaxFiles       = 32
	MaxBundleBytes = 1 << 20
	MinMemoryBytes = 64 << 20
	MinPIDs        = 16
	MaxHeapMiB     = 512
	CompileError   = 1
	ArtifactLimit  = 3
)

var (
	ErrBundle = errors.New("invalid TypeScript bundle")
	ErrLimit  = errors.New("bundle exceeds TypeScript limits")
	pathStem  = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]*(/[A-Za-z0-9_][A-Za-z0-9_-]*)*$`)
)

type File struct {
	Path    string
	Content []byte
}

func JSPath(source string) (string, bool) {
	if !validPath(source, ".ts") {
		return "", false
	}
	return strings.TrimSuffix(source, ".ts") + ".js", true
}

func validPath(name, suffix string) bool {
	stem, ok := strings.CutSuffix(name, suffix)
	return ok && len(name) <= 100 && pathStem.MatchString(stem)
}

func WriteBundle(dst io.Writer, contents fs.FS) error {
	w := tar.NewWriter(dst)
	count, total := 0, 1024
	err := fs.WalkDir(contents, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if !entry.Type().IsRegular() || !validPath(name, ".js") {
			return ErrBundle
		}
		count++
		if count > MaxFiles {
			return ErrLimit
		}
		f, err := contents.Open(name)
		if err != nil {
			return err
		}
		body, err := io.ReadAll(io.LimitReader(f, MaxBundleBytes+1))
		_ = f.Close()
		if err != nil {
			return err
		}
		total += 512 + (len(body)+511)/512*512
		if total > MaxBundleBytes {
			return ErrLimit
		}
		if !utf8.Valid(body) {
			return ErrBundle
		}
		if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(body)), Format: tar.FormatUSTAR}); err != nil {
			return err
		}
		_, err = w.Write(body)
		return err
	})
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrBundle
	}
	return w.Close()
}

func ReadBundle(data []byte, entry string) ([]File, error) {
	var end [1024]byte
	if len(data) < len(end) || len(data) > MaxBundleBytes || !validPath(entry, ".js") ||
		!bytes.Equal(data[len(data)-len(end):], end[:]) {
		return nil, ErrBundle
	}
	input := bytes.NewReader(data)
	r := tar.NewReader(input)
	names := make(map[string]struct{})
	var files []File
	for {
		h, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || len(files) >= MaxFiles || h.Typeflag != tar.TypeReg || h.Format != tar.FormatUSTAR ||
			h.Size < 0 || h.Size > int64(input.Len()) || !validPath(h.Name, ".js") {
			return nil, ErrBundle
		}
		if _, exists := names[h.Name]; exists {
			return nil, ErrBundle
		}
		names[h.Name] = struct{}{}
		start := len(data) - input.Len()
		raw := data[start : start+int(h.Size)]
		if !utf8.Valid(raw) {
			return nil, ErrBundle
		}
		files = append(files, File{Path: h.Name, Content: raw})
	}
	if _, found := names[entry]; !found || input.Len() != 0 {
		return nil, ErrBundle
	}
	return files, nil
}
