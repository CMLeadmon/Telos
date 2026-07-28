package main

// looksLikeUUID reports whether s has canonical 8-4-4-4-12 hex UUID form.
//
// Handlers use this to reject a malformed path or body identifier before it
// reaches a ::uuid cast, so a bad request fails as a 400 rather than a
// database error.
func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
				return false
			}
		}
	}
	return true
}
