package dotnetjob

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"regexp"
)

const (
	DotnetPath     = "/usr/local/share/dotnet/dotnet"
	RuntimeConfig  = "/usr/local/share/sandbox-csharp/runtimeconfig.json"
	CompilerPath   = "/usr/local/bin/sandbox-csharp-compile"
	AssemblyName   = "_sandbox_csharp.dll"
	MaxBytes       = 1 << 20
	MaxSources     = 32
	MinMemoryBytes = 96 << 20
	MinPIDs        = 16
	CompileError   = 1
	ArtifactLimit  = 3
)

var sourcePath = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]*(/[A-Za-z0-9_][A-Za-z0-9_-]*)*\.cs$`)

func ValidSourcePath(path string) bool {
	return len(path) <= 240 && sourcePath.MatchString(path)
}

type clrHeader struct {
	Size       uint32
	Major      uint16
	Minor      uint16
	Metadata   pe.DataDirectory
	Flags      uint32
	EntryPoint uint32
}

// Check the headers emitted by the fixed AnyCPU compiler profile without
// loading the assembly or parsing its untrusted method and symbol tables.
func IsAssembly(data []byte) bool {
	const (
		dosHeaderSize = 64
		peHeaderSize  = 24
		optionalSize  = 224
		sectionSize   = 40
		clrDirectory  = 14
		clrHeaderSize = 72
	)
	if len(data) < dosHeaderSize || len(data) > MaxBytes || string(data[:2]) != "MZ" {
		return false
	}
	offset := int(binary.LittleEndian.Uint32(data[0x3c:]))
	if offset < dosHeaderSize || offset > len(data)-peHeaderSize-optionalSize || string(data[offset:offset+4]) != "PE\x00\x00" {
		return false
	}
	var header pe.FileHeader
	if _, err := binary.Decode(data[offset+4:], binary.LittleEndian, &header); err != nil ||
		header.Machine != pe.IMAGE_FILE_MACHINE_I386 || header.SizeOfOptionalHeader != optionalSize ||
		header.NumberOfSections == 0 || header.NumberOfSections > 16 {
		return false
	}
	var optional pe.OptionalHeader32
	if _, err := binary.Decode(data[offset+peHeaderSize:], binary.LittleEndian, &optional); err != nil ||
		optional.Magic != 0x10b || optional.NumberOfRvaAndSizes <= clrDirectory {
		return false
	}
	sectionsAt := offset + peHeaderSize + optionalSize
	sectionsEnd := sectionsAt + int(header.NumberOfSections)*sectionSize
	if sectionsEnd > len(data) {
		return false
	}
	sections := data[sectionsAt:sectionsEnd]
	directory := optional.DataDirectory[clrDirectory]
	if directory.Size != clrHeaderSize {
		return false
	}
	clr := imageData(data, sections, sectionsEnd, directory)
	var managed clrHeader
	if _, err := binary.Decode(clr, binary.LittleEndian, &managed); err != nil ||
		managed.Size != clrHeaderSize || managed.Flags != 1 ||
		managed.EntryPoint>>24 != 6 || managed.EntryPoint&0xffffff == 0 {
		return false
	}
	meta := imageData(data, sections, sectionsEnd, managed.Metadata)
	return len(meta) >= 16 && bytes.HasPrefix(meta, []byte("BSJB"))
}

func imageData(data, sections []byte, headersEnd int, dir pe.DataDirectory) []byte {
	const sectionSize = 40
	if dir.VirtualAddress == 0 || dir.Size == 0 {
		return nil
	}
	for len(sections) >= sectionSize {
		var section pe.SectionHeader32
		if _, err := binary.Decode(sections[:sectionSize], binary.LittleEndian, &section); err != nil {
			return nil
		}
		sections = sections[sectionSize:]
		if dir.VirtualAddress < section.VirtualAddress {
			continue
		}
		relative := uint64(dir.VirtualAddress - section.VirtualAddress)
		length := uint64(dir.Size)
		if relative+length > uint64(section.SizeOfRawData) {
			continue
		}
		offset := uint64(section.PointerToRawData) + relative
		if uint64(section.PointerToRawData) < uint64(headersEnd) || offset+length > uint64(len(data)) {
			return nil
		}
		return data[offset : offset+length]
	}
	return nil
}
