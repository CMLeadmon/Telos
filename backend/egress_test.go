package main

import "testing"

func TestCoverHostAllowed(t *testing.T) {
	allowed := []string{
		"openlibrary.org",
		"covers.openlibrary.org",
		"OpenLibrary.org",            // case-insensitive
		"covers.openlibrary.org:443", // port stripped
		"image.tmdb.org",
		"books.googleusercontent.com",
		"sub.image.tmdb.org", // subdomain of an allowlisted apex
	}
	for _, h := range allowed {
		if !coverHostAllowed(h) {
			t.Errorf("host %q should be allowed", h)
		}
	}

	denied := []string{
		"",
		"evil.example.com",
		"openlibrary.org.evil.com", // suffix trick
		"notopenlibrary.org",       // not a real subdomain
		"169.254.169.254",          // metadata IP literal
		"localhost",
		"tmdb.org", // apex not on the list (only image.tmdb.org)
	}
	for _, h := range denied {
		if coverHostAllowed(h) {
			t.Errorf("host %q should be denied", h)
		}
	}
}
