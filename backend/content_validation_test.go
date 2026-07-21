package main

import (
	"archive/zip"
	"bytes"
	"testing"
)

func minimalPDF() []byte {
	return []byte("%PDF-1.7\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF\n")
}

func minimalPNG() []byte {
	return append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)
}

func TestValidateUploadContent(t *testing.T) {
	t.Run("valid pdf book", func(t *testing.T) {
		data := minimalPDF()
		info, err := ValidateUploadContent("book.pdf", PurposeBookIngest, bytes.NewReader(data), int64(len(data)))
		if err != nil || info.Category != "pdf" {
			t.Fatalf("valid PDF rejected: %v (%+v)", err, info)
		}
	})

	t.Run("valid epub book", func(t *testing.T) {
		data := buildEPUB(t, validEPUBEntries())
		info, err := ValidateUploadContent("book.epub", PurposeBookIngest, bytes.NewReader(data), int64(len(data)))
		if err != nil || info.Category != "epub" {
			t.Fatalf("valid EPUB rejected: %v (%+v)", err, info)
		}
	})

	t.Run("pdf header without trailer rejected", func(t *testing.T) {
		data := []byte("%PDF-1.7 but no proper trailer here")
		if _, err := ValidateUploadContent("x.pdf", PurposeBookIngest, bytes.NewReader(data), int64(len(data))); err == nil {
			t.Fatal("headerless-trailer PDF accepted")
		}
	})

	t.Run("book that is neither epub nor pdf rejected", func(t *testing.T) {
		data := []byte("just plain text pretending to be a book")
		if _, err := ValidateUploadContent("book.epub", PurposeBookIngest, bytes.NewReader(data), int64(len(data))); err == nil {
			t.Fatal("non-book content accepted for book ingest")
		}
	})

	t.Run("avatar must be an image", func(t *testing.T) {
		png := minimalPNG()
		if _, err := ValidateUploadContent("a.png", PurposeAvatar, bytes.NewReader(png), int64(len(png))); err != nil {
			t.Fatalf("valid PNG avatar rejected: %v", err)
		}
		// A ZIP masquerading as an avatar is rejected regardless of extension.
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		zw.Create("x")
		zw.Close()
		z := buf.Bytes()
		if _, err := ValidateUploadContent("a.png", PurposeAvatar, bytes.NewReader(z), int64(len(z))); err == nil {
			t.Fatal("zip accepted as avatar")
		}
	})

	t.Run("empty content rejected", func(t *testing.T) {
		if _, err := ValidateUploadContent("x", PurposeShared, bytes.NewReader(nil), 0); err == nil {
			t.Fatal("empty content accepted")
		}
	})
}
