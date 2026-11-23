package javaclass

import (
	"archive/zip"
	"bytes"
	"errors"
	"hash/crc32"
	"io"
	"io/fs"
	"regexp"
	"strings"
)

const (
	CompilerPath = "/usr/local/bin/sandbox-java-compile"
	ArchiveName  = "_sandbox_java.jar"
	MaxBytes     = 1 << 20
	MaxSources   = 32
	MaxClasses   = 512

	CompileErrorExit  = 1
	ArtifactLimitExit = 3
)

var (
	ErrInvalid = errors.New("invalid Java class archive")
	ErrLimit   = errors.New("class archive exceeds Java limits")

	sourceName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(/[A-Za-z_][A-Za-z0-9_]*)*\.java$`)
	className  = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*(/[A-Za-z_$][A-Za-z0-9_$]*)*\.class$`)
	java21     = []byte{0xca, 0xfe, 0xba, 0xbe, 0, 0, 0, 65}
)

func MainClass(source string) (string, bool) {
	if len(source) > 240 || !sourceName.MatchString(source) {
		return "", false
	}
	return strings.ReplaceAll(strings.TrimSuffix(source, ".java"), "/", "."), true
}

func WriteArchive(dst io.Writer, classes fs.FS) error {
	w := zip.NewWriter(&boundedWriter{dst: dst, left: MaxBytes})
	count := 0
	err := fs.WalkDir(classes, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		count++
		if count > MaxClasses {
			return ErrLimit
		}
		if len(name) > 240 || !entry.Type().IsRegular() || !className.MatchString(name) {
			return ErrInvalid
		}
		file, err := classes.Open(name)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(file, MaxBytes+1))
		_ = file.Close()
		if err != nil {
			return err
		}
		if len(data) > MaxBytes {
			return ErrLimit
		}
		if !bytes.HasPrefix(data, java21) {
			return ErrInvalid
		}
		size := uint64(len(data))
		out, err := w.CreateRaw(&zip.FileHeader{
			Name: name, Method: zip.Store, CRC32: crc32.ChecksumIEEE(data),
			CompressedSize64: size, UncompressedSize64: size,
		})
		if err != nil {
			return err
		}
		_, err = out.Write(data)
		return err
	})
	if err != nil {
		return err
	}
	return w.Close()
}

func ValidateArchive(data []byte, main string) error {
	if len(data) == 0 || len(data) > MaxBytes {
		return ErrInvalid
	}
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(r.File) == 0 || len(r.File) > MaxClasses || r.Comment != "" {
		return ErrInvalid
	}
	entry := strings.ReplaceAll(main, ".", "/") + ".class"
	found := false
	names := make(map[string]struct{}, len(r.File))
	for _, f := range r.File {
		if len(f.Name) > 240 || !className.MatchString(f.Name) || !f.Mode().IsRegular() ||
			f.Method != zip.Store || f.Flags != 0 || len(f.Extra) != 0 || f.Comment != "" ||
			f.CompressedSize64 != f.UncompressedSize64 || f.UncompressedSize64 > uint64(len(data)) {
			return ErrInvalid
		}
		if _, duplicate := names[f.Name]; duplicate {
			return ErrInvalid
		}
		names[f.Name] = struct{}{}
		start, err := f.DataOffset()
		size := int64(f.UncompressedSize64)
		if err != nil || start < 0 || start > int64(len(data))-size {
			return ErrInvalid
		}
		body := data[start : start+size]
		if !bytes.HasPrefix(body, java21) || crc32.ChecksumIEEE(body) != f.CRC32 {
			return ErrInvalid
		}
		found = found || f.Name == entry
	}
	if !found {
		return ErrInvalid
	}
	return nil
}

type boundedWriter struct {
	dst  io.Writer
	left int
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if len(p) > w.left {
		return 0, ErrLimit
	}
	n, err := w.dst.Write(p)
	w.left -= n
	return n, err
}
