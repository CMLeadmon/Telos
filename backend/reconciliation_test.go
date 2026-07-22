package main

import (
	"context"
	"testing"
)

// fakePhysical is an in-memory PhysicalChecker keyed by content key.
type fakePhysical struct {
	items map[string]struct {
		hash string
		size int64
	}
}

func newFakePhysical() *fakePhysical {
	return &fakePhysical{items: map[string]struct {
		hash string
		size int64
	}{}}
}

func (f *fakePhysical) put(key, hash string, size int64) {
	f.items[key] = struct {
		hash string
		size int64
	}{hash, size}
}

func (f *fakePhysical) Exists(_ StorageArea, key string) (bool, error) {
	_, ok := f.items[key]
	return ok, nil
}

func (f *fakePhysical) HashAndSize(_ StorageArea, key string) (string, int64, error) {
	it := f.items[key]
	return it.hash, it.size, nil
}

func (f *fakePhysical) Remove(_ StorageArea, key string) error {
	delete(f.items, key)
	return nil
}

// fakeCatalog reports which content hashes the book catalog has imported.
type fakeCatalog struct{ imported map[string]string }

func (c *fakeCatalog) LookupImported(_ context.Context, sha256 string) (string, bool, error) {
	id, ok := c.imported[sha256]
	return id, ok, nil
}

func TestAreaForPurpose(t *testing.T) {
	cases := map[string]StorageArea{
		"shared":      "shared",
		"avatar":      "avatars",
		"book_ingest": "bookdrop",
		"unknown":     "shared",
	}
	for purpose, want := range cases {
		if got := areaForPurpose(purpose); got != want {
			t.Errorf("areaForPurpose(%q) = %q, want %q", purpose, got, want)
		}
	}
}

func TestNormalizeFolderName(t *testing.T) {
	ok := map[string]string{
		"Docs":       "docs",
		"  Reports ": "reports",
		"My  Files":  "my files",
	}
	for in, want := range ok {
		norm, _, err := normalizeFolderName(in)
		if err != nil || norm != want {
			t.Errorf("normalizeFolderName(%q) = (%q,%v), want %q", in, norm, err, want)
		}
	}
	for _, bad := range []string{"", "   ", "a/b", "c\\d", "..", ".", string([]byte{0x00}), string([]byte{0x07})} {
		if _, _, err := normalizeFolderName(bad); err == nil {
			t.Errorf("normalizeFolderName(%q) accepted an invalid name", bad)
		}
	}
}
