package main

import (
	"encoding/base64"
	"io/fs"
	"path/filepath"
	"strings"
)

// searchScopeLimit is the maximum authorized results returned per search scope.
const searchScopeLimit = 15

// sharedFileWalkBudget caps how many entries a filesystem search may visit. The
// shared volume is user-controlled and can be arbitrarily deep; search is a
// convenience, so it degrades to partial results rather than stalling a request.
const sharedFileWalkBudget = 5000

// searchSharedFilesystem finds files on the shared volume whose name contains
// term. The SQL half of the file search only sees uploads recorded in the files
// table, but most of a real node's library arrives on disk by other means
// (bookdrop, rsync, Jellyfin imports). Those are listed by the Files browser and
// are shareable, so leaving them unsearchable would make the search box lie.
//
// IDs are base64url(relative path), the same namespace handleListFiles emits
// and resolveSharedFileMeta accepts.
func searchSharedFilesystem(term string, limit int) []SearchFileItem {
	term = strings.ToLower(strings.TrimSpace(term))
	if term == "" || limit <= 0 {
		return nil
	}

	var out []SearchFileItem
	visited := 0
	_ = filepath.WalkDir(mediaRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable subtree skips, it does not abort the walk
		}
		if visited++; visited > sharedFileWalkBudget {
			return fs.SkipAll
		}
		name := d.Name()
		if p != mediaRoot && strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || !strings.Contains(strings.ToLower(name), term) {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		rel, relErr := filepath.Rel(mediaRoot, p)
		if relErr != nil {
			return nil
		}
		out = append(out, SearchFileItem{
			ID:        base64.RawURLEncoding.EncodeToString([]byte(rel)),
			Filename:  name,
			SizeBytes: info.Size(),
			MimeType:  mimeForName(name),
			CreatedAt: info.ModTime(),
		})
		if len(out) >= limit {
			return fs.SkipAll
		}
		return nil
	})
	return out
}

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
