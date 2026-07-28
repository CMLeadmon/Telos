package main

import "strconv"

// itoa is a terse base-10 int formatter shared across the integration tests for
// building deterministic fixture identifiers. It previously lived in the
// now-removed voice_test.go.
func itoa(i int) string { return strconv.Itoa(i) }
