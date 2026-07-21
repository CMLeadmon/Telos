package main

import (
	"bytes"
	"errors"
	"io"
)

// ContentInfo is the validated result of content sniffing.
type ContentInfo struct {
	DetectedMIME string
	Category     string // "epub", "pdf", "image", "generic"
}

var (
	errEmptyContent   = errors.New("content_empty")
	errBadPDF         = errors.New("content_invalid_pdf")
	errFormatMismatch = errors.New("content_format_mismatch")
)

// readAtN reads up to n bytes from offset 0.
func readAtN(r io.ReaderAt, n int) ([]byte, error) {
	buf := make([]byte, n)
	read, err := r.ReadAt(buf, 0)
	if err != nil && err != io.EOF && read == 0 {
		return nil, err
	}
	return buf[:read], nil
}

// sniffMagic detects the content category from leading magic bytes rather than
// trusting the browser-declared MIME or filename extension.
func sniffMagic(head []byte) (mime, category string) {
	switch {
	case bytes.HasPrefix(head, []byte("%PDF-")):
		return "application/pdf", "pdf"
	case len(head) >= 4 && bytes.Equal(head[:4], []byte{0x50, 0x4b, 0x03, 0x04}):
		return "application/zip", "zip" // may be EPUB; confirmed by ValidateEPUB
	case bytes.HasPrefix(head, []byte{0xff, 0xd8, 0xff}):
		return "image/jpeg", "image"
	case bytes.HasPrefix(head, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png", "image"
	default:
		return "application/octet-stream", "generic"
	}
}

// validatePDF checks the header and that a trailer marker is present near the
// end, rejecting a file that merely starts with the header.
func validatePDF(r io.ReaderAt, size int64) error {
	head, err := readAtN(r, 5)
	if err != nil {
		return err
	}
	if !bytes.HasPrefix(head, []byte("%PDF-")) {
		return errBadPDF
	}
	// The %%EOF trailer must appear within the last 2 KiB.
	tailLen := int64(2048)
	if tailLen > size {
		tailLen = size
	}
	tail := make([]byte, tailLen)
	if _, err := r.ReadAt(tail, size-tailLen); err != nil && err != io.EOF {
		return err
	}
	if !bytes.Contains(tail, []byte("%%EOF")) {
		return errBadPDF
	}
	return nil
}

// ValidateUploadContent sniffs and bounds content by purpose. Books
// (book_ingest) must be a valid EPUB or PDF; avatars must be JPEG/PNG; shared
// files get bounded MIME detection (malware scanning is a separate stage).
func ValidateUploadContent(filename string, purpose FilePurpose, r io.ReaderAt, size int64) (ContentInfo, error) {
	if size <= 0 {
		return ContentInfo{}, errEmptyContent
	}
	head, err := readAtN(r, 512)
	if err != nil {
		return ContentInfo{}, err
	}
	mime, category := sniffMagic(head)

	switch purpose {
	case PurposeBookIngest:
		if category == "zip" {
			if err := ValidateEPUB(r, size, DefaultEPUBLimits()); err != nil {
				return ContentInfo{}, err
			}
			return ContentInfo{DetectedMIME: "application/epub+zip", Category: "epub"}, nil
		}
		if category == "pdf" {
			if err := validatePDF(r, size); err != nil {
				return ContentInfo{}, err
			}
			return ContentInfo{DetectedMIME: "application/pdf", Category: "pdf"}, nil
		}
		return ContentInfo{}, errFormatMismatch
	case PurposeAvatar:
		if category != "image" {
			return ContentInfo{}, errFormatMismatch
		}
		return ContentInfo{DetectedMIME: mime, Category: "image"}, nil
	default:
		// Shared files: bounded detection; a PDF is verified structurally.
		if category == "pdf" {
			if err := validatePDF(r, size); err != nil {
				return ContentInfo{}, err
			}
		}
		return ContentInfo{DetectedMIME: mime, Category: category}, nil
	}
}
