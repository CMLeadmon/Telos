# Files Folders & Hierarchical Navigation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add create/delete-folder plus hierarchical (enter-a-folder) navigation to the filesystem-backed Files module, backed by real directories under `/data/shared/media`.

**Architecture:** Per-directory listing. A shared `resolveMediaPath` helper is the traversal-safety boundary; `GET /api/v1/files` gains a `path` param and returns immediate children (folders + files); new `POST /api/v1/folders` and `DELETE /api/v1/folders/{id}` mkdir/rmdir; uploads target the current folder. Frontend gains breadcrumbs, folder rows, and a new-folder control.

**Tech Stack:** Go 1.22 (single-file `backend/main.go`, `net/http` mux, pgx), Next.js 16 static export + React 19 + Zustand, Playwright e2e. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-07-14-files-folders-design.md`

## Global Constraints

- **No changes to the uncommitted WIP in the tree** (settings, voice, logos, etc.). Only these files may be touched: `backend/main.go`, `backend/main_test.go`, `frontend/src/lib/upload.ts`, `frontend/src/stores/useFilesStore.ts`, `frontend/src/app/(shell)/files/page.tsx`, `frontend/src/styles/files.css`, `frontend/e2e/files.spec.ts`. Stage every commit by explicit path — **never `git add -A`/`git add .`**. `backend/main.go`'s only diff vs HEAD is this feature; stage the whole file normally, but if `git diff backend/main.go` ever shows unrelated hunks, stop.
- **Folders are real OS directories** under `mediaRoot` (`/data/shared/media`).
- **Delete refuses non-empty folders** (409); no recursive delete.
- **Permissions:** list = `view_files`; create folder = `upload_files`; delete folder = `manage_files` (mirrors file delete).
- **Path safety:** every caller-supplied path goes through `resolveMediaPath`; reject anything resolving outside `mediaRoot`.
- Go toolchain runs in a container (Go not on host): `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 <cmd>`.
- Frontend has **no unit-test framework** (Playwright e2e only). Per-task frontend verification is `npm run lint && npm run build`; behavior is verified by the e2e task.
- Backend response arrays may be `null` in JSON; the frontend normalizes with the existing `asList`.
- Commit messages end with `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`.

---

### Task 1: Path-safety helper + refactor download/delete

Introduces `mediaRoot` (package var, so tests can point it at a temp dir) and `resolveMediaPath`, and routes the two existing path-based handlers through it. Behavior is unchanged; this is the tested foundation every later task builds on.

**Files:**
- Modify: `backend/main.go` (add helper near `getUniqueFilename` ~line 1970; refactor `handleDownloadFile` ~2300, `handleDeleteFile` ~2345)
- Test: `backend/main_test.go` (append)

**Interfaces:**
- Produces: `var mediaRoot = "/data/shared/media"`; `func resolveMediaPath(rel string) (string, error)` — returns the cleaned absolute path inside `mediaRoot`, or an error if it escapes. `rel == ""` → `mediaRoot`.

- [ ] **Step 1: Preflight — confirm the current tree (incl. WIP) compiles**

Run: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go vet ./...`
Expected: exit 0. If it fails on `settings.go` or other WIP files, **STOP and report** — do not modify WIP; the folder work can't be verified until the tree compiles.

- [ ] **Step 2: Write the failing test**

Append to `backend/main_test.go`:

```go
// ═══════════════════════════════════════════════════════════════════════════
// Media path safety
// ═══════════════════════════════════════════════════════════════════════════

func TestResolveMediaPath(t *testing.T) {
	old := mediaRoot
	mediaRoot = "/data/shared/media"
	defer func() { mediaRoot = old }()

	cases := []struct {
		name    string
		rel     string
		want    string
		wantErr bool
	}{
		{"root", "", "/data/shared/media", false},
		{"child", "vacation", "/data/shared/media/vacation", false},
		{"nested", "vacation/2024", "/data/shared/media/vacation/2024", false},
		{"dirty slashes", "a//b/", "/data/shared/media/a/b", false},
		{"leading slash stays inside", "/etc/passwd", "/data/shared/media/etc/passwd", false},
		{"escape dotdot", "../etc", "", true},
		{"escape nested dotdot", "a/../../x", "", true},
		{"nul byte", "a\x00b", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveMediaPath(c.rel)
			if c.wantErr {
				if err == nil {
					t.Fatalf("resolveMediaPath(%q) = %q, want error", c.rel, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveMediaPath(%q) unexpected error: %v", c.rel, err)
			}
			if got != c.want {
				t.Errorf("resolveMediaPath(%q) = %q, want %q", c.rel, got, c.want)
			}
		})
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go test -run TestResolveMediaPath ./...`
Expected: FAIL to compile — `undefined: mediaRoot`, `undefined: resolveMediaPath`.

- [ ] **Step 4: Add `mediaRoot` and `resolveMediaPath`**

In `backend/main.go`, immediately above `func getUniqueFilename(` (~line 1970), insert:

```go
// mediaRoot is the shared file library's on-disk root. It is a package var so
// tests can point it at a temp directory.
var mediaRoot = "/data/shared/media"

// resolveMediaPath joins a caller-supplied relative path to mediaRoot, cleans
// it, and confirms the result stays within mediaRoot. Returns the absolute
// on-disk path or an error for traversal/escape attempts. rel == "" yields
// mediaRoot itself.
func resolveMediaPath(rel string) (string, error) {
	if strings.ContainsRune(rel, 0) {
		return "", errors.New("invalid path")
	}
	clean := filepath.Clean(filepath.Join(mediaRoot, rel))
	if clean != mediaRoot && !strings.HasPrefix(clean, mediaRoot+string(os.PathSeparator)) {
		return "", errors.New("path escapes media root")
	}
	return clean, nil
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go test -run TestResolveMediaPath ./...`
Expected: PASS.

- [ ] **Step 6: Refactor `handleDownloadFile` to use the helper**

In `handleDownloadFile`, replace the base64 block (currently):

```go
	// Try base64 decoding first (filesystem paths)
	if relPathBytes, err := base64.RawURLEncoding.DecodeString(id); err == nil {
		relPath := string(relPathBytes)
		filePath := filepath.Join("/data/shared/media", relPath)
		cleanPath := filepath.Clean(filePath)
		if strings.HasPrefix(cleanPath, "/data/shared/media") && cleanPath != "/data/shared/media" {
			if info, err := os.Stat(cleanPath); err == nil && !info.IsDir() {
				w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(cleanPath)))
				w.Header().Set("X-Content-Type-Options", "nosniff")
				http.ServeFile(w, r, cleanPath)
				return
			}
		}
	}
```

with:

```go
	// Try base64 decoding first (filesystem paths)
	if relPathBytes, err := base64.RawURLEncoding.DecodeString(id); err == nil {
		if cleanPath, err := resolveMediaPath(string(relPathBytes)); err == nil && cleanPath != mediaRoot {
			if info, err := os.Stat(cleanPath); err == nil && !info.IsDir() {
				w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(cleanPath)))
				w.Header().Set("X-Content-Type-Options", "nosniff")
				http.ServeFile(w, r, cleanPath)
				return
			}
		}
	}
```

- [ ] **Step 7: Refactor `handleDeleteFile` to use the helper**

In `handleDeleteFile`, replace the base64 block (currently):

```go
	// Try base64 decoding first
	if relPathBytes, err := base64.RawURLEncoding.DecodeString(id); err == nil {
		relPath := string(relPathBytes)
		filePath := filepath.Join("/data/shared/media", relPath)
		cleanPath := filepath.Clean(filePath)
		if strings.HasPrefix(cleanPath, "/data/shared/media") && cleanPath != "/data/shared/media" {
			if info, err := os.Stat(cleanPath); err == nil && !info.IsDir() {
				if err := os.Remove(cleanPath); err != nil {
					http.Error(w, "Failed to delete file from disk", http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]string{"status": "success"})
				return
			}
		}
	}
```

with:

```go
	// Try base64 decoding first
	if relPathBytes, err := base64.RawURLEncoding.DecodeString(id); err == nil {
		if cleanPath, err := resolveMediaPath(string(relPathBytes)); err == nil && cleanPath != mediaRoot {
			if info, err := os.Stat(cleanPath); err == nil && !info.IsDir() {
				if err := os.Remove(cleanPath); err != nil {
					http.Error(w, "Failed to delete file from disk", http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]string{"status": "success"})
				return
			}
		}
	}
```

- [ ] **Step 8: Verify build, vet, and full test suite**

Run: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 sh -c "go build ./... && go vet ./... && go test ./..."`
Expected: all pass (existing tests + `TestResolveMediaPath`).

- [ ] **Step 9: Commit**

```bash
git add backend/main.go backend/main_test.go
git commit -m "refactor(backend): extract resolveMediaPath, route file download/delete through it"
```

---

### Task 2: Per-directory file listing

Rewrites `handleListFiles` from a global recursive walk to an `os.ReadDir` of one directory, returning folders + files for the requested `path`.

**Files:**
- Modify: `backend/main.go` (`handleListFiles` ~line 1988–2105; add `mimeForName` helper)
- Test: `backend/main_test.go` (append)

**Interfaces:**
- Consumes: `mediaRoot`, `resolveMediaPath` (Task 1).
- Produces: `GET /api/v1/files?path=<rel>&page=N` → JSON object `{ path: string, folders: [{id,name,path}], files: [{id,filename,sha256,uploader_id,scan_status,size_bytes,mime_type,created_at}], page: int, hasNext: bool }`. `filename` is the basename; folder/file `id` is `base64url(relPathFromRoot)`. Also `func mimeForName(name string) string`.

- [ ] **Step 1: Write the failing test**

Append to `backend/main_test.go`:

```go
// ═══════════════════════════════════════════════════════════════════════════
// Per-directory file listing
// ═══════════════════════════════════════════════════════════════════════════

func TestHandleListFilesReturnsFoldersAndFiles(t *testing.T) {
	old := mediaRoot
	mediaRoot = t.TempDir()
	defer func() { mediaRoot = old }()

	if err := os.Mkdir(filepath.Join(mediaRoot, "vacation"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mediaRoot, "root.txt"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mediaRoot, "vacation", "beach.jpg"), []byte("img"), 0644); err != nil {
		t.Fatal(err)
	}

	// Root listing: one folder (vacation), one file (root.txt); beach.jpg is NOT here.
	req := httptest.NewRequest("GET", "/api/v1/files?path=&page=1", nil)
	rr := httptest.NewRecorder()
	handleListFiles(rr, req)
	if rr.Code != 200 {
		t.Fatalf("root list status = %d, want 200", rr.Code)
	}
	var root struct {
		Path    string `json:"path"`
		Folders []struct {
			ID, Name, Path string
		} `json:"folders"`
		Files []struct {
			ID, Filename string
		} `json:"files"`
		HasNext bool `json:"hasNext"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &root); err != nil {
		t.Fatalf("unmarshal root: %v (body %s)", err, rr.Body.String())
	}
	if len(root.Folders) != 1 || root.Folders[0].Name != "vacation" {
		t.Fatalf("root folders = %+v, want one 'vacation'", root.Folders)
	}
	if len(root.Files) != 1 || root.Files[0].Filename != "root.txt" {
		t.Fatalf("root files = %+v, want one 'root.txt'", root.Files)
	}

	// Enter vacation: beach.jpg present, as a basename.
	req2 := httptest.NewRequest("GET", "/api/v1/files?path=vacation&page=1", nil)
	rr2 := httptest.NewRecorder()
	handleListFiles(rr2, req2)
	var sub struct {
		Files []struct{ Filename string } `json:"files"`
	}
	if err := json.Unmarshal(rr2.Body.Bytes(), &sub); err != nil {
		t.Fatalf("unmarshal sub: %v", err)
	}
	if len(sub.Files) != 1 || sub.Files[0].Filename != "beach.jpg" {
		t.Fatalf("vacation files = %+v, want one 'beach.jpg'", sub.Files)
	}

	// Bad path → 400.
	reqBad := httptest.NewRequest("GET", "/api/v1/files?path=../etc&page=1", nil)
	rrBad := httptest.NewRecorder()
	handleListFiles(rrBad, reqBad)
	if rrBad.Code != 400 {
		t.Fatalf("escaping path status = %d, want 400", rrBad.Code)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go test -run TestHandleListFilesReturnsFoldersAndFiles ./...`
Expected: FAIL — current `handleListFiles` returns a bare array, so `root.Folders` is empty / unmarshal shape mismatch, and the escaping-path case returns 200 not 400.

- [ ] **Step 3: Add the `mimeForName` helper**

In `backend/main.go`, immediately above `func handleListFiles(` (~line 1988), insert:

```go
// mimeForName maps a filename's extension to a MIME type for library listings.
func mimeForName(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mp4":
		return "video/mp4"
	case ".mkv":
		return "video/x-matroska"
	case ".webm":
		return "video/webm"
	case ".mp3":
		return "audio/mpeg"
	case ".m4a":
		return "audio/mp4"
	case ".wav":
		return "audio/wav"
	case ".ogg":
		return "audio/ogg"
	case ".pdf":
		return "application/pdf"
	case ".epub":
		return "application/epub+zip"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}
```

- [ ] **Step 4: Replace the body of `handleListFiles`**

Replace the entire `func handleListFiles(w http.ResponseWriter, r *http.Request) { … }` (from its `func` line through its closing brace, ~1988–2105) with:

```go
func handleListFiles(w http.ResponseWriter, r *http.Request) {
	relPath := r.URL.Query().Get("path")
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1
	}
	const limit = 20

	dir, err := resolveMediaPath(relPath)
	if err != nil {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "Folder not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to read directory", http.StatusInternalServerError)
		return
	}

	type folderEntry struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Path string `json:"path"`
	}
	type fileEntry struct {
		ID         string    `json:"id"`
		Filename   string    `json:"filename"`
		SHA256     string    `json:"sha256"`
		UploaderID string    `json:"uploader_id"`
		ScanStatus string    `json:"scan_status"`
		SizeBytes  int64     `json:"size_bytes"`
		MimeType   string    `json:"mime_type"`
		CreatedAt  time.Time `json:"created_at"`
	}

	folders := []folderEntry{}
	files := []fileEntry{}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		rel := filepath.Join(relPath, name)
		id := base64.RawURLEncoding.EncodeToString([]byte(rel))
		if e.IsDir() {
			folders = append(folders, folderEntry{ID: id, Name: name, Path: rel})
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileEntry{
			ID:         id,
			Filename:   name,
			SHA256:     "",
			UploaderID: "",
			ScanStatus: "clean",
			SizeBytes:  info.Size(),
			MimeType:   mimeForName(name),
			CreatedAt:  info.ModTime(),
		})
	}

	sort.Slice(folders, func(i, j int) bool { return folders[i].Name < folders[j].Name })
	sort.Slice(files, func(i, j int) bool { return files[i].CreatedAt.After(files[j].CreatedAt) })

	// Paginate over the combined (folders-first) sequence.
	total := len(folders) + len(files)
	offset := (page - 1) * limit
	end := offset + limit
	if offset > total {
		offset = total
	}
	if end > total {
		end = total
	}
	pageFolders := []folderEntry{}
	pageFiles := []fileEntry{}
	for i := offset; i < end; i++ {
		if i < len(folders) {
			pageFolders = append(pageFolders, folders[i])
		} else {
			pageFiles = append(pageFiles, files[i-len(folders)])
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"path":    relPath,
		"folders": pageFolders,
		"files":   pageFiles,
		"page":    page,
		"hasNext": end < total,
	})
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go test -run TestHandleListFilesReturnsFoldersAndFiles ./...`
Expected: PASS.

- [ ] **Step 6: Verify build, vet, full suite (catches any now-unused import)**

Run: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 sh -c "go build ./... && go vet ./... && go test ./..."`
Expected: all pass. (`io/fs` remains used elsewhere; `sort` is still used above — no import removal expected. If the build reports an unused import, remove exactly that import line.)

- [ ] **Step 7: Commit**

```bash
git add backend/main.go backend/main_test.go
git commit -m "feat(backend): per-directory file listing with folders + files"
```

---

### Task 3: Create & delete folder endpoints

**Files:**
- Modify: `backend/main.go` (add two handlers after `handleDeleteFile`; add two routes after the file routes ~line 246)
- Test: `backend/main_test.go` (append)

**Interfaces:**
- Consumes: `mediaRoot`, `resolveMediaPath` (Task 1).
- Produces: `func handleCreateFolder(w, r)`, `func handleDeleteFolder(w, r)`; routes `POST /api/v1/folders` (`upload_files`), `DELETE /api/v1/folders/{id}` (`manage_files`). Create body `{path,name}` → 201 `{id,name,path}`. Delete `{id}` (base64url rel) → 200 `{status}`; non-empty → 409.

- [ ] **Step 1: Write the failing test**

Append to `backend/main_test.go`:

```go
// ═══════════════════════════════════════════════════════════════════════════
// Folder create / delete
// ═══════════════════════════════════════════════════════════════════════════

func TestHandleCreateFolder(t *testing.T) {
	old := mediaRoot
	mediaRoot = t.TempDir()
	defer func() { mediaRoot = old }()

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/v1/folders", strings.NewReader(body))
		rr := httptest.NewRecorder()
		handleCreateFolder(rr, req)
		return rr
	}

	if rr := post(`{"path":"","name":"docs"}`); rr.Code != 201 {
		t.Fatalf("create status = %d, want 201 (body %s)", rr.Code, rr.Body.String())
	}
	if info, err := os.Stat(filepath.Join(mediaRoot, "docs")); err != nil || !info.IsDir() {
		t.Fatal("expected docs/ to exist on disk")
	}
	if rr := post(`{"path":"","name":"docs"}`); rr.Code != 409 {
		t.Fatalf("duplicate status = %d, want 409", rr.Code)
	}
	if rr := post(`{"path":"","name":".."}`); rr.Code != 400 {
		t.Fatalf("dotdot name status = %d, want 400", rr.Code)
	}
	if rr := post(`{"path":"","name":"a/b"}`); rr.Code != 400 {
		t.Fatalf("slashed name status = %d, want 400", rr.Code)
	}
}

func TestHandleDeleteFolderEmptyVsNonEmpty(t *testing.T) {
	old := mediaRoot
	mediaRoot = t.TempDir()
	defer func() { mediaRoot = old }()

	if err := os.Mkdir(filepath.Join(mediaRoot, "empty"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(mediaRoot, "full"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mediaRoot, "full", "x.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	del := func(rel string) *httptest.ResponseRecorder {
		id := base64.RawURLEncoding.EncodeToString([]byte(rel))
		req := httptest.NewRequest("DELETE", "/api/v1/folders/"+id, nil)
		req.SetPathValue("id", id)
		rr := httptest.NewRecorder()
		handleDeleteFolder(rr, req)
		return rr
	}

	if rr := del("full"); rr.Code != 409 {
		t.Fatalf("non-empty delete status = %d, want 409", rr.Code)
	}
	if _, err := os.Stat(filepath.Join(mediaRoot, "full")); err != nil {
		t.Fatal("non-empty folder should still exist")
	}
	if rr := del("empty"); rr.Code != 200 {
		t.Fatalf("empty delete status = %d, want 200", rr.Code)
	}
	if _, err := os.Stat(filepath.Join(mediaRoot, "empty")); !os.IsNotExist(err) {
		t.Fatal("empty folder should be gone")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go test -run 'TestHandleCreateFolder|TestHandleDeleteFolderEmptyVsNonEmpty' ./...`
Expected: FAIL to compile — `undefined: handleCreateFolder`, `undefined: handleDeleteFolder`.

- [ ] **Step 3: Add the two handlers**

In `backend/main.go`, immediately after the closing brace of `handleDeleteFile`, insert:

```go
func handleCreateFolder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || len(name) > 255 || name == "." || name == ".." ||
		strings.ContainsAny(name, "/\x00") {
		http.Error(w, "Invalid folder name", http.StatusBadRequest)
		return
	}

	parent, err := resolveMediaPath(body.Path)
	if err != nil {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	if info, err := os.Stat(parent); err != nil || !info.IsDir() {
		http.Error(w, "Parent folder not found", http.StatusNotFound)
		return
	}

	target := filepath.Join(parent, name)
	if err := os.Mkdir(target, 0755); err != nil {
		if os.IsExist(err) {
			http.Error(w, "A folder with that name already exists", http.StatusConflict)
			return
		}
		http.Error(w, "Failed to create folder", http.StatusInternalServerError)
		return
	}

	rel, err := filepath.Rel(mediaRoot, target)
	if err != nil {
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	id := base64.RawURLEncoding.EncodeToString([]byte(rel))
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": id, "name": name, "path": rel})
}

func handleDeleteFolder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	relBytes, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil {
		http.Error(w, "Invalid folder ID", http.StatusBadRequest)
		return
	}
	target, err := resolveMediaPath(string(relBytes))
	if err != nil || target == mediaRoot {
		http.Error(w, "Invalid folder", http.StatusBadRequest)
		return
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		http.Error(w, "Folder not found", http.StatusNotFound)
		return
	}

	// Refuse non-empty folders (rmdir semantics) — check before removing.
	children, err := os.ReadDir(target)
	if err != nil {
		http.Error(w, "Failed to read folder", http.StatusInternalServerError)
		return
	}
	if len(children) > 0 {
		http.Error(w, "Folder isn't empty", http.StatusConflict)
		return
	}
	if err := os.Remove(target); err != nil {
		http.Error(w, "Failed to delete folder", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}
```

- [ ] **Step 4: Register the routes**

In `backend/main.go`, immediately after the line
`mux.Handle("DELETE /api/v1/files/{id}", withAuth(http.HandlerFunc(handleDeleteFile), "manage_files"))` (~line 246), insert:

```go
	mux.Handle("POST /api/v1/folders", withAuth(http.HandlerFunc(handleCreateFolder), "upload_files"))
	mux.Handle("DELETE /api/v1/folders/{id}", withAuth(http.HandlerFunc(handleDeleteFolder), "manage_files"))
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go test -run 'TestHandleCreateFolder|TestHandleDeleteFolderEmptyVsNonEmpty' ./...`
Expected: PASS.

- [ ] **Step 6: Verify build, vet, full suite**

Run: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 sh -c "go build ./... && go vet ./... && go test ./..."`
Expected: all pass.

- [ ] **Step 7: Commit**

```bash
git add backend/main.go backend/main_test.go
git commit -m "feat(backend): create + delete-if-empty folder endpoints"
```

---

### Task 4: Upload into the current folder

Threads a `path` multipart field through the media-library upload branch so files land in the current folder; extracts a tested `mediaUploadTarget` helper.

**Files:**
- Modify: `backend/main.go` (`processUploadWithLimits` ~2131–2298; add `mediaUploadTarget`)
- Test: `backend/main_test.go` (append)

**Interfaces:**
- Consumes: `mediaRoot`, `resolveMediaPath`, `getUniqueFilename`.
- Produces: `func mediaUploadTarget(relDir, filename string) (destDir, destKey, fileID string, err error)` — `destDir` absolute, `destKey` collision-resolved basename, `fileID = base64url(relPathFromRoot)`.

- [ ] **Step 1: Write the failing test**

Append to `backend/main_test.go`:

```go
// ═══════════════════════════════════════════════════════════════════════════
// Upload targeting
// ═══════════════════════════════════════════════════════════════════════════

func TestMediaUploadTarget(t *testing.T) {
	old := mediaRoot
	mediaRoot = t.TempDir()
	defer func() { mediaRoot = old }()

	if err := os.Mkdir(filepath.Join(mediaRoot, "sub"), 0755); err != nil {
		t.Fatal(err)
	}

	// Root target.
	destDir, destKey, fileID, err := mediaUploadTarget("", "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if destDir != mediaRoot || destKey != "a.txt" {
		t.Fatalf("root target = (%q,%q), want (%q,a.txt)", destDir, destKey, mediaRoot)
	}
	if want := base64.RawURLEncoding.EncodeToString([]byte("a.txt")); fileID != want {
		t.Fatalf("root fileID = %q, want %q", fileID, want)
	}

	// Subfolder target with collision.
	if err := os.WriteFile(filepath.Join(mediaRoot, "sub", "a.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	destDir, destKey, fileID, err = mediaUploadTarget("sub", "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if destDir != filepath.Join(mediaRoot, "sub") || destKey != "a_1.txt" {
		t.Fatalf("sub target = (%q,%q), want (.../sub, a_1.txt)", destDir, destKey)
	}
	if want := base64.RawURLEncoding.EncodeToString([]byte(filepath.Join("sub", "a_1.txt"))); fileID != want {
		t.Fatalf("sub fileID = %q, want %q", fileID, want)
	}

	// Escaping path → error.
	if _, _, _, err := mediaUploadTarget("../etc", "a.txt"); err == nil {
		t.Fatal("expected error for escaping path")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go test -run TestMediaUploadTarget ./...`
Expected: FAIL to compile — `undefined: mediaUploadTarget`.

- [ ] **Step 3: Add the `mediaUploadTarget` helper**

In `backend/main.go`, immediately above `func getUniqueFilename(` (just below `resolveMediaPath` from Task 1), insert:

```go
// mediaUploadTarget resolves where a media-library upload should land given a
// caller-supplied relative directory, returning the absolute destination dir,
// a collision-resolved basename, and the base64url file ID (relative to root).
func mediaUploadTarget(relDir, filename string) (destDir, destKey, fileID string, err error) {
	destDir, err = resolveMediaPath(relDir)
	if err != nil {
		return "", "", "", err
	}
	destKey = getUniqueFilename(destDir, filename)
	rel, err := filepath.Rel(mediaRoot, filepath.Join(destDir, destKey))
	if err != nil {
		return "", "", "", err
	}
	fileID = base64.RawURLEncoding.EncodeToString([]byte(rel))
	return destDir, destKey, fileID, nil
}
```

- [ ] **Step 4: Read the upload `path` field and use the helper**

In `processUploadWithLimits`, just after the `r.ParseMultipartForm(maxBytes)` error check (~line 2137), add:

```go
	relDir := r.FormValue("path")
```

Then, in the promote block, replace the media branch and the `destPath` line. Current:

```go
	// File is clean, promote atomically
	var destDir string
	var destKey string
	if isBook {
		destDir = "/data/shared/bookdrop"
		destKey = fileHash + ext
	} else if !writeResponse {
		destDir = "/data/shared/staging/library"
		destKey = fileHash + ext
	} else {
		destDir = "/data/shared/media"
		destKey = getUniqueFilename(destDir, header.Filename)
	}

	destPath := filepath.Join(destDir, destKey)
```

Replace with:

```go
	// File is clean, promote atomically
	var destDir string
	var destKey string
	var mediaFileID string
	if isBook {
		destDir = "/data/shared/bookdrop"
		destKey = fileHash + ext
	} else if !writeResponse {
		destDir = "/data/shared/staging/library"
		destKey = fileHash + ext
	} else {
		var terr error
		destDir, destKey, mediaFileID, terr = mediaUploadTarget(relDir, header.Filename)
		if terr != nil {
			os.Remove(tempPath)
			http.Error(w, "Invalid upload path", http.StatusBadRequest)
			return "", false
		}
	}

	destPath := filepath.Join(destDir, destKey)
```

- [ ] **Step 5: Use the precomputed media file ID**

Further down, replace the ID-assignment block. Current:

```go
	// Insert into DB (skip for standard media library files)
	var fileID string
	if !isBook && writeResponse {
		fileID = base64.RawURLEncoding.EncodeToString([]byte(destKey))
	} else {
```

Replace with:

```go
	// Insert into DB (skip for standard media library files)
	var fileID string
	if !isBook && writeResponse {
		fileID = mediaFileID
	} else {
```

- [ ] **Step 6: Run the target test + full suite**

Run: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 sh -c "go test -run TestMediaUploadTarget ./... && go build ./... && go vet ./... && go test ./..."`
Expected: all pass.

- [ ] **Step 7: Commit**

```bash
git add backend/main.go backend/main_test.go
git commit -m "feat(backend): upload into the current folder via path field"
```

---

### Task 5: Frontend data layer — upload path + folder-aware store

**Files:**
- Modify: `frontend/src/lib/upload.ts`
- Modify: `frontend/src/stores/useFilesStore.ts`

**Interfaces:**
- Consumes: backend `GET /api/v1/files?path=&page=`, `POST /api/v1/folders`, `DELETE /api/v1/folders/{id}` (Tasks 2–3), `POST /api/v1/files` with a `path` field (Task 4).
- Produces (for Task 6): store fields `path: string`, `folders: FolderEntry[]`; actions `fetchDir(path, page)`, `enterFolder(path)`, `navigateTo(path)`, `createFolder(name)`, `deleteFolder(id)`; retained `fetchPage`, `deleteFile`, `upload(file, destination)`, `dismissUpload`, `setNotice`; type `FolderEntry = { id; name; path }`. `uploadFile(path, file, onProgress, extra?)`.

- [ ] **Step 1: Add an `extra` fields param to `uploadFile`**

In `frontend/src/lib/upload.ts`, change the `uploadFile` signature and body. Replace:

```typescript
export function uploadFile(
  path: string,
  file: File,
  onProgress: (phase: UploadPhase, percent: number) => void,
): Promise<UploadOutcome> {
```

with:

```typescript
export function uploadFile(
  path: string,
  file: File,
  onProgress: (phase: UploadPhase, percent: number) => void,
  extra?: Record<string, string>,
): Promise<UploadOutcome> {
```

and replace the FormData construction near the end:

```typescript
    const form = new FormData();
    form.append("file", file);
    xhr.send(form);
```

with:

```typescript
    const form = new FormData();
    form.append("file", file);
    for (const [k, v] of Object.entries(extra ?? {})) {
      form.append(k, v);
    }
    xhr.send(form);
```

- [ ] **Step 2: Rewrite the store**

Replace the entire contents of `frontend/src/stores/useFilesStore.ts` with:

```typescript
import { create } from "zustand";
import { api, ApiError } from "@/lib/api";
import { uploadFile, UploadError } from "@/lib/upload";

export interface FileEntry {
  id: string;
  filename: string;
  sha256: string;
  uploader_id: string;
  scan_status: string;
  size_bytes: number;
  mime_type: string;
  created_at: string;
}

export interface FolderEntry {
  id: string;
  name: string;
  path: string;
}

interface DirResponse {
  path: string;
  folders: FolderEntry[] | null;
  files: FileEntry[] | null;
  page: number;
  hasNext: boolean;
}

export type UploadDestination = "library" | "bookdrop";

export type UploadState =
  | { phase: "uploading"; percent: number }
  | { phase: "scanning" }
  | { phase: "error"; message: string };

export interface ActiveUpload {
  key: number;
  filename: string;
  destination: UploadDestination;
  state: UploadState;
}

export const PAGE_SIZE = 20;
export const MAX_UPLOAD_BYTES = 104857600; // 100 MiB, mirrors the gateway cap

// Mirrors the gateway's processUpload allowlists exactly.
const LIBRARY_EXTENSIONS = [
  ".pdf", ".epub", ".jpg", ".jpeg", ".png", ".webp",
  ".mp3", ".m4a", ".ogg", ".wav", ".mp4", ".webm",
];
const BOOK_EXTENSIONS = [".pdf", ".epub"];

export function validateFile(
  file: File,
  destination: UploadDestination,
): string | null {
  const dot = file.name.lastIndexOf(".");
  const ext = dot === -1 ? "" : file.name.slice(dot).toLowerCase();
  const allowed =
    destination === "bookdrop" ? BOOK_EXTENSIONS : LIBRARY_EXTENSIONS;
  if (!allowed.includes(ext)) {
    return destination === "bookdrop"
      ? "bookdrop only accepts .pdf and .epub"
      : `file type ${ext || "(none)"} is not allowed`;
  }
  if (file.size > MAX_UPLOAD_BYTES) return "file exceeds the 100 MiB limit";
  return null;
}

// The gateway marshals empty Go slices as JSON null — normalize before use.
function asList<T>(value: T[] | null | undefined): T[] {
  return Array.isArray(value) ? value : [];
}

let uploadCounter = 0;

interface FilesState {
  path: string;
  folders: FolderEntry[];
  files: FileEntry[];
  page: number;
  status: "idle" | "loading" | "ready" | "error";
  error: string | null;
  hasNextPage: boolean;
  uploads: ActiveUpload[];
  notice: string | null;
  fetchDir: (path: string, page: number) => Promise<void>;
  fetchPage: (page: number) => Promise<void>;
  enterFolder: (path: string) => Promise<void>;
  navigateTo: (path: string) => Promise<void>;
  createFolder: (name: string) => Promise<void>;
  deleteFolder: (id: string) => Promise<void>;
  deleteFile: (id: string) => Promise<void>;
  upload: (file: File, destination: UploadDestination) => Promise<void>;
  dismissUpload: (key: number) => void;
  setNotice: (notice: string | null) => void;
}

export const useFilesStore = create<FilesState>()((set, get) => ({
  path: "",
  folders: [],
  files: [],
  page: 1,
  status: "idle",
  error: null,
  hasNextPage: false,
  uploads: [],
  notice: null,

  fetchDir: async (path, page) => {
    set({ status: "loading", error: null });
    try {
      const res = await api<DirResponse>(
        `/api/v1/files?path=${encodeURIComponent(path)}&page=${page}`,
      );
      set({
        path: res.path ?? path,
        folders: asList(res.folders),
        files: asList(res.files),
        page,
        status: "ready",
        hasNextPage: Boolean(res.hasNext),
      });
    } catch (err) {
      set({
        status: "error",
        error: err instanceof Error ? err.message : "failed to load files",
      });
    }
  },

  fetchPage: async (page) => {
    await get().fetchDir(get().path, page);
  },

  enterFolder: async (path) => {
    await get().fetchDir(path, 1);
  },

  navigateTo: async (path) => {
    await get().fetchDir(path, 1);
  },

  createFolder: async (name) => {
    try {
      await api("/api/v1/folders", {
        method: "POST",
        body: JSON.stringify({ path: get().path, name }),
      });
      await get().fetchDir(get().path, 1);
    } catch (err) {
      if (err instanceof ApiError && err.status === 403) {
        set({ notice: "you don't have permission to create folders" });
      } else if (err instanceof ApiError && err.status === 409) {
        set({ notice: "a folder with that name already exists" });
      } else {
        set({
          notice: err instanceof Error ? err.message : "couldn't create folder",
        });
      }
    }
  },

  deleteFolder: async (id) => {
    try {
      await api(`/api/v1/folders/${id}`, { method: "DELETE" });
      await get().fetchDir(get().path, get().page);
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        set({ notice: "folder isn't empty — clear it first" });
      } else if (err instanceof ApiError && err.status === 403) {
        set({ notice: "you don't have permission to delete folders" });
      } else {
        set({
          notice: err instanceof Error ? err.message : "delete failed",
        });
      }
    }
  },

  deleteFile: async (id) => {
    try {
      await api(`/api/v1/files/${id}`, { method: "DELETE" });
      const { path, page, folders, files } = get();
      // Deleting the last row of a later page: step back a page.
      const target =
        folders.length + files.length === 1 && page > 1 ? page - 1 : page;
      await get().fetchDir(path, target);
    } catch (err) {
      if (err instanceof ApiError && err.status === 403) {
        set({ notice: "you don't have permission to delete files" });
      } else {
        set({
          notice: err instanceof Error ? err.message : "delete failed",
        });
      }
    }
  },

  upload: async (file, destination) => {
    const key = ++uploadCounter;
    const entry: ActiveUpload = {
      key,
      filename: file.name,
      destination,
      state: { phase: "uploading", percent: 0 },
    };
    set((s) => ({ uploads: [...s.uploads, entry] }));
    const patch = (state: UploadState) =>
      set((s) => ({
        uploads: s.uploads.map((u) => (u.key === key ? { ...u, state } : u)),
      }));

    // Library uploads land in the current folder; bookdrop stays flat.
    const path =
      destination === "bookdrop" ? "/api/v1/files/books" : "/api/v1/files";
    const extra =
      destination === "bookdrop" ? undefined : { path: get().path };
    try {
      await uploadFile(
        path,
        file,
        (phase, percent) =>
          patch(
            phase === "scanning"
              ? { phase: "scanning" }
              : { phase: "uploading", percent },
          ),
        extra,
      );
      set((s) => ({ uploads: s.uploads.filter((u) => u.key !== key) }));
      await get().fetchDir(get().path, 1);
    } catch (err) {
      let message = "upload failed";
      if (err instanceof UploadError) {
        if (err.status === 403)
          message = "you don't have permission to upload here";
        else if (err.status === 422) {
          message = "rejected by security scan";
          void get().fetchDir(get().path, 1);
        } else if (err.status === 503)
          message = "security scanner unavailable — try again later";
        else if (err.message) message = err.message;
      }
      patch({ phase: "error", message });
    }
  },

  dismissUpload: (key) =>
    set((s) => ({ uploads: s.uploads.filter((u) => u.key !== key) })),

  setNotice: (notice) => set({ notice }),
}));
```

- [ ] **Step 3: Verify lint + build**

Run (from `frontend/`): `npm run lint && npm run build`
Expected: both exit 0. (The existing `files/page.tsx` still compiles: it uses `files`, `page`, `status`, `error`, `hasNextPage`, `fetchPage`, `deleteFile`, `upload`, `dismissUpload`, `setNotice`, `notice` — all retained.)

- [ ] **Step 4: Commit**

```bash
git add frontend/src/lib/upload.ts frontend/src/stores/useFilesStore.ts
git commit -m "feat(frontend): folder-aware files store + upload path field"
```

---

### Task 6: Frontend view — breadcrumbs, folder rows, new-folder

**Files:**
- Modify: `frontend/src/app/(shell)/files/page.tsx`
- Modify: `frontend/src/styles/files.css`

**Interfaces:**
- Consumes: everything Task 5 produces; `apiBase`; `VaporwaveScene`.
- Produces test hooks for Task 7: `data-testid="files-breadcrumbs"`, `data-testid="new-folder-btn"`, `data-testid="new-folder-input"`, folder rows `data-testid="folder-row"`, existing `files-table`/`file-row`/`files-dropzone`/`files-input`/`files-empty`.

- [ ] **Step 1: Add folder + breadcrumb styles**

Append to `frontend/src/styles/files.css`:

```css
/* Breadcrumbs + folder rows */
.crumbs{display:flex;align-items:center;gap:6px;flex-wrap:wrap;font-family:var(--f-mono);
  font-size:12px;color:var(--muted)}
.crumbs .crumb{color:var(--muted);padding:2px 6px;border-radius:var(--r-xs)}
.crumbs .crumb:hover{color:var(--ink)}
.crumbs .crumb.here{color:var(--ink);font-weight:600}
.crumbs .sep{color:var(--faint)}

.newfolderbar{display:flex;align-items:center;gap:8px}
.newfolderbar input{background:var(--input);border:1px solid var(--line);color:var(--ink);
  border-radius:var(--r);padding:8px 12px;outline:none;font-size:13px;min-width:200px}
.newfolderbar input:focus{border-color:var(--accent)}

.frow.folder{cursor:pointer}
.frow.folder:hover{background:var(--surface-2)}
.frow.folder .ficon{color:var(--accent)}
```

- [ ] **Step 2: Rewrite `files/page.tsx`**

Replace the entire contents of `frontend/src/app/(shell)/files/page.tsx` with:

```tsx
"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import {
  BookOpen,
  Check,
  ChevronLeft,
  ChevronRight,
  Download,
  File as FileIcon,
  FileText,
  Film,
  Folder,
  FolderPlus,
  Image as ImageIcon,
  Music,
  Trash2,
  UploadCloud,
  X,
} from "lucide-react";
import { apiBase } from "@/lib/api";
import {
  type FileEntry,
  type FolderEntry,
  type UploadDestination,
  useFilesStore,
  validateFile,
} from "@/stores/useFilesStore";
import { VaporwaveScene } from "@/components/VaporwaveScene";

function mimeIcon(mime: string) {
  if (mime.startsWith("image/")) return <ImageIcon size={17} />;
  if (mime.startsWith("audio/")) return <Music size={17} />;
  if (mime.startsWith("video/")) return <Film size={17} />;
  if (mime === "application/pdf" || mime === "application/epub+zip")
    return <FileText size={17} />;
  return <FileIcon size={17} />;
}

function humanSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KiB", "MiB", "GiB"];
  let v = bytes / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v >= 10 ? Math.round(v) : v.toFixed(1)} ${units[i]}`;
}

function shortDate(iso: string): string {
  return new Date(iso).toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
}

function FolderRow({
  folder,
  confirming,
  onEnter,
  onAskDelete,
  onCancelDelete,
  onDelete,
}: {
  folder: FolderEntry;
  confirming: boolean;
  onEnter: () => void;
  onAskDelete: () => void;
  onCancelDelete: () => void;
  onDelete: () => void;
}) {
  return (
    <div className="frow folder" data-testid="folder-row" onClick={onEnter}>
      <span className="ficon">
        <Folder size={17} />
      </span>
      <span className="fname" title={folder.name}>
        {folder.name}
      </span>
      <span className="fmeta">—</span>
      <span className="fmeta" />
      <span className="fmeta">folder</span>
      <span className="facts" onClick={(e) => e.stopPropagation()}>
        {confirming ? (
          <>
            <button
              className="iconbtn danger"
              aria-label={`confirm delete ${folder.name}`}
              onClick={onDelete}
            >
              <Check size={16} />
            </button>
            <button
              className="iconbtn"
              aria-label="cancel delete"
              onClick={onCancelDelete}
            >
              <X size={16} />
            </button>
          </>
        ) : (
          <button
            className="iconbtn danger"
            aria-label={`delete folder ${folder.name}`}
            onClick={onAskDelete}
          >
            <Trash2 size={16} />
          </button>
        )}
      </span>
    </div>
  );
}

function FileRow({
  file,
  confirming,
  onAskDelete,
  onCancelDelete,
  onDelete,
}: {
  file: FileEntry;
  confirming: boolean;
  onAskDelete: () => void;
  onCancelDelete: () => void;
  onDelete: () => void;
}) {
  const infected = file.scan_status !== "clean";
  return (
    <div className={`frow${infected ? " infected" : ""}`} data-testid="file-row">
      <span className="ficon">{mimeIcon(file.mime_type)}</span>
      <span className="fname" title={file.filename}>
        {file.filename}
      </span>
      <span className="fmeta">{humanSize(file.size_bytes)}</span>
      <span className={`scanpill ${infected ? "infected" : "clean"}`}>
        {file.scan_status}
      </span>
      <span className="fmeta">{shortDate(file.created_at)}</span>
      <span className="facts">
        {confirming ? (
          <>
            <button
              className="iconbtn danger"
              aria-label={`confirm delete ${file.filename}`}
              onClick={onDelete}
            >
              <Check size={16} />
            </button>
            <button
              className="iconbtn"
              aria-label="cancel delete"
              onClick={onCancelDelete}
            >
              <X size={16} />
            </button>
          </>
        ) : (
          <>
            {!infected && (
              <a
                className="iconbtn"
                aria-label={`download ${file.filename}`}
                href={`${apiBase()}/api/v1/files/${file.id}/download`}
              >
                <Download size={16} />
              </a>
            )}
            <button
              className="iconbtn danger"
              aria-label={`delete ${file.filename}`}
              onClick={onAskDelete}
            >
              <Trash2 size={16} />
            </button>
          </>
        )}
      </span>
    </div>
  );
}

function Breadcrumbs({
  path,
  onNavigate,
}: {
  path: string;
  onNavigate: (path: string) => void;
}) {
  const segments = path ? path.split("/") : [];
  return (
    <div className="crumbs" data-testid="files-breadcrumbs">
      <button
        className={`crumb${segments.length === 0 ? " here" : ""}`}
        onClick={() => onNavigate("")}
      >
        Home
      </button>
      {segments.map((seg, i) => {
        const target = segments.slice(0, i + 1).join("/");
        return (
          <span key={target} style={{ display: "contents" }}>
            <span className="sep">/</span>
            <button
              className={`crumb${i === segments.length - 1 ? " here" : ""}`}
              onClick={() => onNavigate(target)}
            >
              {seg}
            </button>
          </span>
        );
      })}
    </div>
  );
}

export default function FilesPage() {
  const {
    path,
    folders,
    files,
    page,
    status,
    error,
    hasNextPage,
    uploads,
    notice,
    fetchDir,
    fetchPage,
    enterFolder,
    navigateTo,
    createFolder,
    deleteFolder,
    deleteFile,
    upload,
    dismissUpload,
    setNotice,
  } = useFilesStore();
  const [destination, setDestination] = useState<UploadDestination>("library");
  const [dragging, setDragging] = useState(false);
  const [confirmingId, setConfirmingId] = useState<string | null>(null);
  const [creatingFolder, setCreatingFolder] = useState(false);
  const [folderName, setFolderName] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (status === "idle") void fetchDir("", 1);
  }, [status, fetchDir]);

  const takeFiles = useCallback(
    (list: FileList | null) => {
      if (!list || list.length === 0) return;
      if (list.length > 1)
        setNotice("one file at a time — uploading the first only");
      const file = list[0];
      const problem = validateFile(file, destination);
      if (problem) {
        setNotice(problem);
        return;
      }
      void upload(file, destination);
    },
    [destination, upload, setNotice],
  );

  const submitFolder = () => {
    const name = folderName.trim();
    if (name) void createFolder(name);
    setFolderName("");
    setCreatingFolder(false);
  };

  const hasRows = folders.length > 0 || files.length > 0;
  const uploadTarget = path ? `/${path}` : "root";

  return (
    <>
      <VaporwaveScene />
      <div className="arenahead">
        <div className="name">
          <Folder size={17} />
          Files
        </div>
        <span className="kicker">{`// page ${page}`}</span>
      </div>
      <div className="banner">
        every byte is scanned before it touches the shelf
      </div>

      <div className="fwrap">
        <div className="destrow" style={{ justifyContent: "space-between" }}>
          <Breadcrumbs path={path} onNavigate={(p) => void navigateTo(p)} />
          {creatingFolder ? (
            <div className="newfolderbar">
              <input
                autoFocus
                data-testid="new-folder-input"
                placeholder="folder name"
                value={folderName}
                onChange={(e) => setFolderName(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") submitFolder();
                  if (e.key === "Escape") {
                    setFolderName("");
                    setCreatingFolder(false);
                  }
                }}
              />
              <button
                className="iconbtn"
                aria-label="confirm new folder"
                onClick={submitFolder}
              >
                <Check size={16} />
              </button>
            </div>
          ) : (
            <button
              className="btn-ghost btn-sm"
              data-testid="new-folder-btn"
              onClick={() => setCreatingFolder(true)}
            >
              <FolderPlus size={15} /> New folder
            </button>
          )}
        </div>

        {notice && (
          <div className="fnotice" role="status">
            <span>{notice}</span>
            <button
              className="iconbtn"
              aria-label="dismiss notice"
              onClick={() => setNotice(null)}
            >
              <X size={15} />
            </button>
          </div>
        )}

        <div
          className={`dropzone${dragging ? " drag" : ""}`}
          data-testid="files-dropzone"
          onClick={() => inputRef.current?.click()}
          onDragOver={(e) => {
            e.preventDefault();
            setDragging(true);
          }}
          onDragLeave={() => setDragging(false)}
          onDrop={(e) => {
            e.preventDefault();
            setDragging(false);
            takeFiles(e.dataTransfer.files);
          }}
        >
          <UploadCloud size={26} />
          <div>
            drop a file here or <b>click to browse</b>
          </div>
          <span className="dz-hint">
            {destination === "bookdrop"
              ? "pdf / epub → bookdrop (flat), max 100 MiB"
              : `uploading to ${uploadTarget} — max 100 MiB`}
          </span>
          <input
            ref={inputRef}
            type="file"
            hidden
            data-testid="files-input"
            onChange={(e) => {
              takeFiles(e.target.files);
              e.target.value = "";
            }}
          />
        </div>

        <div className="destrow">
          <span className="kicker">{"// destination"}</span>
          <button
            className={`chip${destination === "library" ? " on" : ""}`}
            onClick={() => setDestination("library")}
          >
            <Folder size={13} /> library
          </button>
          <button
            className={`chip${destination === "bookdrop" ? " on" : ""}`}
            onClick={() => setDestination("bookdrop")}
          >
            <BookOpen size={13} /> bookdrop
          </button>
        </div>

        {uploads.map((u) => (
          <div
            key={u.key}
            className={`uprow${u.state.phase === "error" ? " err" : ""}`}
          >
            <span className="upname">{u.filename}</span>
            {u.state.phase === "uploading" && (
              <>
                <span className="upbar">
                  <b style={{ width: `${u.state.percent}%` }} />
                </span>
                <span className="upstate">{u.state.percent}%</span>
              </>
            )}
            {u.state.phase === "scanning" && (
              <>
                <span className="upbar scan">
                  <b />
                </span>
                <span className="upstate">scanning…</span>
              </>
            )}
            {u.state.phase === "error" && (
              <>
                <span className="upstate">{u.state.message}</span>
                <button
                  className="iconbtn"
                  aria-label="dismiss upload"
                  onClick={() => dismissUpload(u.key)}
                >
                  <X size={15} />
                </button>
              </>
            )}
          </div>
        ))}

        {status === "error" && (
          <div className="placeholder">
            <h2>Shelf unreachable.</h2>
            <p>{error}</p>
            <button
              className="btn-ghost btn-sm"
              onClick={() => fetchPage(page)}
            >
              retry
            </button>
          </div>
        )}

        {status === "ready" && !hasRows && (
          <div className="placeholder" data-testid="files-empty">
            <h2>{path ? "Empty folder." : "Nothing on the shelf."}</h2>
            <p>drop something above, or make a folder to organize.</p>
          </div>
        )}

        {hasRows && (
          <div className="ftable" data-testid="files-table">
            <div className="frow head">
              <span />
              <span>name</span>
              <span>size</span>
              <span>scan</span>
              <span>added</span>
              <span style={{ textAlign: "right" }}>actions</span>
            </div>
            {folders.map((f) => (
              <FolderRow
                key={f.id}
                folder={f}
                confirming={confirmingId === f.id}
                onEnter={() => {
                  setConfirmingId(null);
                  void enterFolder(f.path);
                }}
                onAskDelete={() => setConfirmingId(f.id)}
                onCancelDelete={() => setConfirmingId(null)}
                onDelete={() => {
                  setConfirmingId(null);
                  void deleteFolder(f.id);
                }}
              />
            ))}
            {files.map((f) => (
              <FileRow
                key={f.id}
                file={f}
                confirming={confirmingId === f.id}
                onAskDelete={() => setConfirmingId(f.id)}
                onCancelDelete={() => setConfirmingId(null)}
                onDelete={() => {
                  setConfirmingId(null);
                  void deleteFile(f.id);
                }}
              />
            ))}
          </div>
        )}

        {(page > 1 || hasNextPage) && (
          <div className="fpager">
            <button
              className="iconbtn"
              aria-label="previous page"
              disabled={page <= 1 || status === "loading"}
              onClick={() => fetchPage(page - 1)}
            >
              <ChevronLeft size={17} />
            </button>
            <span className="pageno">page {page}</span>
            <button
              className="iconbtn"
              aria-label="next page"
              disabled={!hasNextPage || status === "loading"}
              onClick={() => fetchPage(page + 1)}
            >
              <ChevronRight size={17} />
            </button>
          </div>
        )}
      </div>
    </>
  );
}
```

- [ ] **Step 3: Verify lint + build**

Run (from `frontend/`): `npm run lint && npm run build`
Expected: both exit 0.

- [ ] **Step 4: Commit**

```bash
git add "frontend/src/app/(shell)/files/page.tsx" frontend/src/styles/files.css
git commit -m "feat(frontend): folder navigation UI — breadcrumbs, folder rows, new folder"
```

---

### Task 7: E2E + full-stack verification

**Files:**
- Modify: `frontend/e2e/files.spec.ts` (append a folders test)

**Interfaces:**
- Consumes: all prior tasks; the credentialed login helper already in `files.spec.ts`; test hooks from Task 6.

- [ ] **Step 1: Append the folders e2e test**

Add to `frontend/e2e/files.spec.ts`, after the existing test (reuse the file's existing `login` helper, `PNG` buffer, and `USERNAME`/`PASSWORD` guards — do not redeclare them):

```typescript
test("folders: create, enter, upload inside, scope, delete", async ({ page }) => {
  await login(page);
  await page.goto("/files/");
  await expect(page.getByTestId("files-dropzone")).toBeVisible();

  const folder = `e2e-dir-${Math.random().toString(36).slice(2, 8)}`;
  const fileName = `inside-${Math.random().toString(36).slice(2, 8)}.png`;

  // Create a folder.
  await page.getByTestId("new-folder-btn").click();
  await page.getByTestId("new-folder-input").fill(folder);
  await page.getByTestId("new-folder-input").press("Enter");
  const folderRow = page
    .getByTestId("folder-row")
    .filter({ hasText: folder })
    .first();
  await expect(folderRow).toBeVisible({ timeout: 10_000 });

  // Enter it and upload a file inside.
  await folderRow.click();
  await expect(page.getByTestId("files-breadcrumbs")).toContainText(folder);
  await page.getByTestId("files-input").setInputFiles({
    name: fileName,
    mimeType: "image/png",
    buffer: PNG,
  });
  const fileRow = page.getByTestId("file-row").filter({ hasText: fileName });
  await expect(fileRow).toBeVisible({ timeout: 30_000 }); // upload + scan

  // Scope: the file is NOT visible back at root.
  await page.getByRole("button", { name: "Home" }).click();
  await expect(page.getByTestId("file-row").filter({ hasText: fileName })).toHaveCount(0);

  // Deleting the non-empty folder is refused with a notice.
  const rootFolderRow = page
    .getByTestId("folder-row")
    .filter({ hasText: folder })
    .first();
  await rootFolderRow.getByLabel(`delete folder ${folder}`).click();
  await rootFolderRow.getByLabel(`confirm delete ${folder}`).click();
  await expect(page.locator(".fnotice")).toContainText("isn't empty");

  // Empty it (enter, delete the file), then the folder deletes cleanly.
  await rootFolderRow.click();
  await expect(page.getByTestId("files-breadcrumbs")).toContainText(folder);
  const innerRow = page.getByTestId("file-row").filter({ hasText: fileName });
  await innerRow.getByLabel(`delete ${fileName}`).click();
  await innerRow.getByLabel(`confirm delete ${fileName}`).click();
  await expect(page.getByTestId("file-row").filter({ hasText: fileName })).toHaveCount(0, {
    timeout: 15_000,
  });

  await page.getByRole("button", { name: "Home" }).click();
  const finalRow = page.getByTestId("folder-row").filter({ hasText: folder }).first();
  await finalRow.getByLabel(`delete folder ${folder}`).click();
  await finalRow.getByLabel(`confirm delete ${folder}`).click();
  await expect(page.getByTestId("folder-row").filter({ hasText: folder })).toHaveCount(0, {
    timeout: 10_000,
  });
});
```

- [ ] **Step 2: Rebuild and redeploy telos-core**

The running gateway must serve the new backend. Rebuild and force-recreate (a plain `up -d --build` does NOT swap a running container onto a new image on this host):

```bash
podman-compose up -d --build telos-core
podman-compose up -d --force-recreate telos-core
```

Verify the container runs the freshly built image:
```bash
test "$(podman inspect telos-core --format '{{.Image}}')" = "$(podman inspect localhost/telos_telos-core:latest --format '{{.Id}}')" && echo OK-image-current
curl -s http://localhost:8080/api/v1/health
```
Expected: `OK-image-current` and a healthy JSON body. If the build fails on WIP (`settings.go` etc.), **STOP and report** — do not modify WIP.

- [ ] **Step 3: Ensure a credentialed test account (Librarian) exists**

Per memory `telos-e2e-test-accounts`: mint via the invite flow with an `Origin: http://localhost:8080` header, then grant `Librarian` (needs the user's OK for the RBAC grant). Use `E2E_USERNAME`/`E2E_PASSWORD` for that account. If one already exists from a prior session, reuse it.

- [ ] **Step 4: Run the Files e2e alone (avoid the shared-account parallelism race)**

With `npm run dev` running (port 3000) and the gateway up:
```bash
cd frontend && E2E_USERNAME=<user> E2E_PASSWORD=<pass> npx playwright test files
```
Expected: 2 passed (the original files test + the new folders test). Per memory, run this spec in isolation, not the whole suite.

- [ ] **Step 5: Confirm real directories on disk + no mock fallbacks**

```bash
podman exec telos-core sh -c 'ls -la /data/shared/media' 
podman logs --since 10m telos-core 2>&1 | grep -i "mock\|fallback" || echo "no mock warnings"
```
Expected: created folders appear during the run and are gone after (the e2e cleans up); no mock-fallback warnings.

- [ ] **Step 6: Frontend gate**

Run (from `frontend/`): `npm run lint && npm run build`
Expected: both exit 0.

- [ ] **Step 7: Commit**

```bash
git add frontend/e2e/files.spec.ts
git commit -m "test(frontend): e2e for folder create/enter/upload-scope/delete"
```

---

## Final verification (whole feature)

- [ ] Backend: `go build ./... && go vet ./... && go test ./...` all green in the container.
- [ ] Frontend: `npm run lint && npm run build` clean; `npx playwright test files` → 2 passed.
- [ ] Manual smoke in a browser at `http://localhost:3000/files/`: create a nested folder chain (a → b), upload into `b`, breadcrumb-jump back to `a` then Home, confirm the file only shows inside `b`, delete file then both folders.
- [ ] `git status` shows the WIP (settings, voice, logos) **unchanged** and unstaged exactly as before; only the seven allowed files were committed across the 7 tasks.
- [ ] `podman logs telos-core` shows no mock-fallback warnings from the session.

## Notes / edge cases baked in

- **Traversal:** every path flows through `resolveMediaPath`; `../` escapes → 400. Covered by `TestResolveMediaPath`.
- **Empty folders** are first-class (listing returns directory entries directly), unlike the old recursive-walk which only surfaced files.
- **Non-empty delete** is refused by an explicit `os.ReadDir` emptiness check → 409 (no `syscall` import needed).
- **Bookdrop** uploads stay flat and ignore the current folder (the store omits the `path` field for bookdrop); the drop-zone hint says so.
- **Response shape change** (`array` → object) only bites once the gateway is rebuilt in Task 7; until then the running gateway serves the old shape and the store isn't pointed at the new backend. That's why the rebuild and the e2e live in the same task.
```
