package main

import (
	"archive/zip"
	"bytes"
	"testing"
)

type epubEntry struct {
	name  string
	body  string
	store bool
}

func buildEPUB(t *testing.T, entries []epubEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		method := zip.Deflate
		if e.store {
			method = zip.Store
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: e.name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(e.body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const validContainer = `<?xml version="1.0"?>
<container><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`

func validEPUBEntries() []epubEntry {
	return []epubEntry{
		{name: "mimetype", body: "application/epub+zip", store: true},
		{name: "META-INF/container.xml", body: validContainer},
		{name: "OEBPS/content.opf", body: "<package/>"},
	}
}

func TestValidateEPUB(t *testing.T) {
	limits := DefaultEPUBLimits()

	t.Run("valid", func(t *testing.T) {
		data := buildEPUB(t, validEPUBEntries())
		if err := ValidateEPUB(bytes.NewReader(data), int64(len(data)), limits); err != nil {
			t.Fatalf("valid EPUB rejected: %v", err)
		}
	})

	t.Run("compressed mimetype", func(t *testing.T) {
		e := validEPUBEntries()
		e[0].store = false // deflate
		data := buildEPUB(t, e)
		if err := ValidateEPUB(bytes.NewReader(data), int64(len(data)), limits); err == nil {
			t.Fatal("compressed mimetype accepted")
		}
	})

	t.Run("reordered mimetype", func(t *testing.T) {
		e := []epubEntry{
			{name: "META-INF/container.xml", body: validContainer},
			{name: "mimetype", body: "application/epub+zip", store: true},
		}
		data := buildEPUB(t, e)
		if err := ValidateEPUB(bytes.NewReader(data), int64(len(data)), limits); err == nil {
			t.Fatal("mimetype not first accepted")
		}
	})

	t.Run("wrong mimetype content", func(t *testing.T) {
		e := validEPUBEntries()
		e[0].body = "application/zip"
		data := buildEPUB(t, e)
		if err := ValidateEPUB(bytes.NewReader(data), int64(len(data)), limits); err == nil {
			t.Fatal("wrong mimetype accepted")
		}
	})

	t.Run("missing container", func(t *testing.T) {
		e := []epubEntry{{name: "mimetype", body: "application/epub+zip", store: true}}
		data := buildEPUB(t, e)
		if err := ValidateEPUB(bytes.NewReader(data), int64(len(data)), limits); err == nil {
			t.Fatal("missing container accepted")
		}
	})

	t.Run("missing rootfile", func(t *testing.T) {
		e := []epubEntry{
			{name: "mimetype", body: "application/epub+zip", store: true},
			{name: "META-INF/container.xml", body: validContainer},
			// OEBPS/content.opf omitted
		}
		data := buildEPUB(t, e)
		if err := ValidateEPUB(bytes.NewReader(data), int64(len(data)), limits); err == nil {
			t.Fatal("missing rootfile accepted")
		}
	})

	t.Run("traversal entry", func(t *testing.T) {
		e := append(validEPUBEntries(), epubEntry{name: "../escape.txt", body: "x"})
		data := buildEPUB(t, e)
		if err := ValidateEPUB(bytes.NewReader(data), int64(len(data)), limits); err == nil {
			t.Fatal("traversal entry accepted")
		}
	})

	t.Run("external entity in container", func(t *testing.T) {
		e := validEPUBEntries()
		e[1].body = `<?xml version="1.0"?><!DOCTYPE x [<!ENTITY xxe SYSTEM "file:///etc/passwd">]>` + validContainer
		data := buildEPUB(t, e)
		if err := ValidateEPUB(bytes.NewReader(data), int64(len(data)), limits); err == nil {
			t.Fatal("XXE container accepted")
		}
	})

	t.Run("too many entries", func(t *testing.T) {
		tiny := limits
		tiny.MaxEntries = 2
		data := buildEPUB(t, validEPUBEntries()) // 3 entries
		if err := ValidateEPUB(bytes.NewReader(data), int64(len(data)), tiny); err == nil {
			t.Fatal("entry-count limit not enforced")
		}
	})

	t.Run("expansion limit", func(t *testing.T) {
		tiny := limits
		tiny.MaxUncompressedBytes = 5
		e := validEPUBEntries()
		e = append(e, epubEntry{name: "OEBPS/big.txt", body: "way more than five bytes"})
		data := buildEPUB(t, e)
		if err := ValidateEPUB(bytes.NewReader(data), int64(len(data)), tiny); err == nil {
			t.Fatal("expansion limit not enforced")
		}
	})

	t.Run("not a zip", func(t *testing.T) {
		data := []byte("not a zip file at all")
		if err := ValidateEPUB(bytes.NewReader(data), int64(len(data)), limits); err == nil {
			t.Fatal("non-zip accepted as EPUB")
		}
	})
}
