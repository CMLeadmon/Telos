package main

import (
	"archive/zip"
	"errors"
	"io"
	"strings"
)

// EPUBLimits bounds archive expansion to resist zip bombs.
type EPUBLimits struct {
	MaxEntries           int
	MaxUncompressedBytes int64
	MaxEntryBytes        int64
	MaxCompressionRatio  float64
}

// DefaultEPUBLimits are the beta bounds.
func DefaultEPUBLimits() EPUBLimits {
	return EPUBLimits{
		MaxEntries:           10000,
		MaxUncompressedBytes: 500 << 20,
		MaxEntryBytes:        100 << 20,
		MaxCompressionRatio:  100.0,
	}
}

var (
	errEPUBNotZip       = errors.New("epub_not_zip")
	errEPUBMimetype     = errors.New("epub_bad_mimetype")
	errEPUBTooManyFiles = errors.New("epub_too_many_entries")
	errEPUBTooLarge     = errors.New("epub_expansion_too_large")
	errEPUBRatio        = errors.New("epub_compression_ratio")
	errEPUBTraversal    = errors.New("epub_entry_traversal")
	errEPUBNoContainer  = errors.New("epub_missing_container")
	errEPUBNoRootfile   = errors.New("epub_missing_rootfile")
)

// ValidateEPUB confirms the OCF structure: the first entry is an uncompressed
// `mimetype` file with the exact EPUB media type, a bounded META-INF/container.xml
// names a confined rootfile, and archive expansion stays within limits. It never
// leaks archive internals in its error categories.
func ValidateEPUB(r io.ReaderAt, size int64, limits EPUBLimits) error {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return errEPUBNotZip
	}
	if len(zr.File) == 0 {
		return errEPUBNotZip
	}
	if len(zr.File) > limits.MaxEntries {
		return errEPUBTooManyFiles
	}

	// 1. First entry must be an uncompressed "mimetype" with the exact type.
	first := zr.File[0]
	if first.Name != "mimetype" || first.Method != zip.Store {
		return errEPUBMimetype
	}
	mt, err := readZipEntry(first, limits.MaxEntryBytes)
	if err != nil {
		return errEPUBMimetype
	}
	if strings.TrimRight(string(mt), "\r\n ") != "application/epub+zip" {
		return errEPUBMimetype
	}

	// 2. Bound expansion and reject traversal/absolute entry names.
	var total int64
	haveContainer := false
	for _, f := range zr.File {
		name := f.Name
		if strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
			return errEPUBTraversal
		}
		us := int64(f.UncompressedSize64)
		if us > limits.MaxEntryBytes {
			return errEPUBTooLarge
		}
		total += us
		if total > limits.MaxUncompressedBytes {
			return errEPUBTooLarge
		}
		if f.CompressedSize64 > 0 {
			ratio := float64(f.UncompressedSize64) / float64(f.CompressedSize64)
			if ratio > limits.MaxCompressionRatio {
				return errEPUBRatio
			}
		}
		if name == "META-INF/container.xml" {
			haveContainer = true
		}
	}
	if !haveContainer {
		return errEPUBNoContainer
	}

	// 3. The container must name a confined, present rootfile (package doc).
	rootfile, err := epubRootfilePath(zr, limits.MaxEntryBytes)
	if err != nil {
		return err
	}
	if !zipHasEntry(zr, rootfile) {
		return errEPUBNoRootfile
	}
	return nil
}

func readZipEntry(f *zip.File, max int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, max))
}

func zipHasEntry(zr *zip.Reader, name string) bool {
	for _, f := range zr.File {
		if f.Name == name {
			return true
		}
	}
	return false
}

// epubRootfilePath extracts the first rootfile full-path from container.xml
// using bounded, entity-free scanning (no XML external entity expansion).
func epubRootfilePath(zr *zip.Reader, max int64) (string, error) {
	var container *zip.File
	for _, f := range zr.File {
		if f.Name == "META-INF/container.xml" {
			container = f
			break
		}
	}
	if container == nil {
		return "", errEPUBNoContainer
	}
	data, err := readZipEntry(container, max)
	if err != nil {
		return "", errEPUBNoContainer
	}
	// Reject external entity declarations outright rather than parsing them.
	if strings.Contains(string(data), "<!ENTITY") || strings.Contains(string(data), "<!DOCTYPE") {
		return "", errEPUBNoRootfile
	}
	// Extract full-path="..." from the first <rootfile> element without a full
	// XML parse (avoids entity expansion and is bounded).
	s := string(data)
	idx := strings.Index(s, "full-path")
	if idx < 0 {
		return "", errEPUBNoRootfile
	}
	rest := s[idx:]
	q := strings.IndexAny(rest, `"'`)
	if q < 0 {
		return "", errEPUBNoRootfile
	}
	rest = rest[q+1:]
	end := strings.IndexAny(rest, `"'`)
	if end < 0 {
		return "", errEPUBNoRootfile
	}
	rootfile := rest[:end]
	if rootfile == "" || strings.HasPrefix(rootfile, "/") || strings.Contains(rootfile, "..") {
		return "", errEPUBTraversal
	}
	return rootfile, nil
}
