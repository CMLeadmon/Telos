package main

import "strings"

// searchScopeLimit is the maximum authorized results returned per search scope.
const searchScopeLimit = 15

// normalizedSearchTerm validates a search term (2–128 characters) and returns
// a lowercased %substring% pattern that matches the lower(col) trigram GIN
// index expressions.
func normalizedSearchTerm(q string) (string, bool) {
	q = strings.TrimSpace(q)
	if len(q) < 2 || len(q) > 128 {
		return "", false
	}
	// Escape LIKE metacharacters so a term is matched literally.
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + replacer.Replace(strings.ToLower(q)) + "%", true
}
