package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateAnnotationLocator(t *testing.T) {
	cases := []struct {
		name, format, in string
		ok               bool
	}{
		{"epub valid", "EPUB", `{"cfi":"epubcfi(/6/4!/4)"}`, true},
		{"epub empty cfi", "EPUB", `{"cfi":""}`, false},
		{"epub with pdf fields", "EPUB", `{"cfi":"x","page":2}`, false},
		{"pdf valid", "PDF", `{"page":3,"rects":[{"x":0.1,"y":0.1,"w":0.2,"h":0.05}]}`, true},
		{"pdf no rects", "PDF", `{"page":3,"rects":[]}`, false},
		{"pdf page zero", "PDF", `{"page":0,"rects":[{"x":0,"y":0,"w":0.1,"h":0.1}]}`, false},
		{"pdf rect out of range", "PDF", `{"page":1,"rects":[{"x":0.9,"y":0,"w":0.5,"h":0.1}]}`, false},
		{"pdf with epub field", "PDF", `{"page":1,"cfi":"x","rects":[{"x":0,"y":0,"w":0.1,"h":0.1}]}`, false},
		{"unknown format", "MOBI", `{"cfi":"x"}`, false},
	}
	for _, c := range cases {
		_, err := validateAnnotationLocator(c.format, json.RawMessage(c.in))
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v, want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestValidateAnnotationText(t *testing.T) {
	if err := validateAnnotationText("ok", "ok"); err != nil {
		t.Fatalf("valid text rejected: %v", err)
	}
	if err := validateAnnotationText(strings.Repeat("x", 2001), ""); err == nil {
		t.Fatal("oversized selected text accepted")
	}
	if err := validateAnnotationText("", strings.Repeat("x", 10001)); err == nil {
		t.Fatal("oversized note accepted")
	}
}
