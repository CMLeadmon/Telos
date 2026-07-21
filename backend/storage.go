//go:build linux

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strings"

	"golang.org/x/sys/unix"
)

// PromotionResult reports the outcome of a promote.
type PromotionResult struct {
	CrossDevice bool
	SHA256      string
	Size        int64
}

// StorageRef names an asset by storage area and key.
type StorageRef struct {
	Area StorageArea
	Key  string
}

// ConfinedStorage resolves every operation beneath a set of trusted root
// directory descriptors using openat2, so a user-supplied key can never escape
// its area via traversal, symlinks, or magic links.
type ConfinedStorage struct {
	roots map[StorageArea]int // area -> O_PATH dir fd
}

// resolveFlags forbids any symlink or magic-link and confines resolution
// beneath the root.
const resolveFlags = unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS

// storageKeyRE bounds keys to a safe charset with no traversal.
var storageKeyRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,250}$`)

var errUnsafeKey = errors.New("unsafe storage key")

func validKey(key string) bool {
	if !storageKeyRE.MatchString(key) {
		return false
	}
	if strings.Contains(key, "..") || strings.HasPrefix(key, "/") || strings.HasSuffix(key, "/") {
		return false
	}
	for _, part := range strings.Split(key, "/") {
		if part == "" {
			return false
		}
	}
	return true
}

// OpenConfinedStorage opens each root once as an O_PATH directory descriptor,
// failing on a kernel without openat2 support rather than falling back to
// lexical confinement.
func OpenConfinedStorage(roots map[StorageArea]string) (*ConfinedStorage, error) {
	s := &ConfinedStorage{roots: map[StorageArea]int{}}
	for area, dir := range roots {
		fd, err := unix.Open(dir, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			s.Close()
			return nil, fmt.Errorf("open storage root %q: %w", dir, err)
		}
		s.roots[area] = fd
	}
	// Preflight: confirm openat2 works on the first root.
	for _, fd := range s.roots {
		how := &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_DIRECTORY, Resolve: resolveFlags}
		if probe, err := unix.Openat2(fd, ".", how); err == nil {
			unix.Close(probe)
		} else if !errors.Is(err, unix.ENOENT) {
			s.Close()
			return nil, fmt.Errorf("openat2 unsupported on this kernel: %w", err)
		}
		break
	}
	return s, nil
}

func (s *ConfinedStorage) Close() {
	for _, fd := range s.roots {
		unix.Close(fd)
	}
	s.roots = nil
}

func (s *ConfinedStorage) rootFD(area StorageArea) (int, error) {
	fd, ok := s.roots[area]
	if !ok {
		return 0, fmt.Errorf("unknown storage area %q", area)
	}
	return fd, nil
}

// mkdirParents creates each parent component of key beneath the root, confined.
func (s *ConfinedStorage) mkdirParents(rootFD int, key string) error {
	dir := path.Dir(key)
	if dir == "." || dir == "/" {
		return nil
	}
	cur := rootFD
	opened := []int{}
	defer func() {
		for _, fd := range opened {
			unix.Close(fd)
		}
	}()
	for _, part := range strings.Split(dir, "/") {
		// Create the component (ignore EEXIST), then open it confined.
		if err := unix.Mkdirat(cur, part, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
			return err
		}
		how := &unix.OpenHow{Flags: unix.O_PATH | unix.O_DIRECTORY, Resolve: resolveFlags}
		next, err := unix.Openat2(cur, part, how)
		if err != nil {
			return err
		}
		opened = append(opened, next)
		cur = next
	}
	return nil
}

// CreateExclusive creates a new file at key beneath area, failing if it exists.
func (s *ConfinedStorage) CreateExclusive(area StorageArea, key string, perm fs.FileMode) (*os.File, error) {
	if !validKey(key) {
		return nil, errUnsafeKey
	}
	rootFD, err := s.rootFD(area)
	if err != nil {
		return nil, err
	}
	if err := s.mkdirParents(rootFD, key); err != nil {
		return nil, err
	}
	how := &unix.OpenHow{
		Flags:   unix.O_CREAT | unix.O_EXCL | unix.O_WRONLY | unix.O_CLOEXEC,
		Mode:    uint64(perm.Perm()),
		Resolve: resolveFlags,
	}
	fd, err := unix.Openat2(rootFD, key, how)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), key), nil
}

// OpenRead opens an existing file read-only, confined.
func (s *ConfinedStorage) OpenRead(area StorageArea, key string) (*os.File, error) {
	if !validKey(key) {
		return nil, errUnsafeKey
	}
	rootFD, err := s.rootFD(area)
	if err != nil {
		return nil, err
	}
	how := &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_CLOEXEC, Resolve: resolveFlags}
	fd, err := unix.Openat2(rootFD, key, how)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), key), nil
}

// Remove unlinks a file confined to its area.
func (s *ConfinedStorage) Remove(area StorageArea, key string) error {
	if !validKey(key) {
		return errUnsafeKey
	}
	rootFD, err := s.rootFD(area)
	if err != nil {
		return err
	}
	// Resolve the parent directory confined, then unlink the leaf by name.
	dir, leaf := path.Split(key)
	parentFD := rootFD
	if dir != "" {
		how := &unix.OpenHow{Flags: unix.O_PATH | unix.O_DIRECTORY, Resolve: resolveFlags}
		pfd, err := unix.Openat2(rootFD, strings.TrimSuffix(dir, "/"), how)
		if err != nil {
			return err
		}
		defer unix.Close(pfd)
		parentFD = pfd
	}
	if err := unix.Unlinkat(parentFD, leaf, 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return err
	}
	return nil
}

// PromoteNoReplace moves source to destination with no-replace semantics,
// verifying the SHA-256. Same-filesystem uses renameat2(RENAME_NOREPLACE);
// cross-device copies to an exclusive temp, syncs, and renames no-replace.
func (s *ConfinedStorage) PromoteNoReplace(ctx context.Context, source, destination StorageRef, expectedSHA256 string) (PromotionResult, error) {
	if !validKey(source.Key) || !validKey(destination.Key) {
		return PromotionResult{}, errUnsafeKey
	}
	srcRoot, err := s.rootFD(source.Area)
	if err != nil {
		return PromotionResult{}, err
	}
	dstRoot, err := s.rootFD(destination.Area)
	if err != nil {
		return PromotionResult{}, err
	}

	// Verify the source hash before promoting.
	sum, size, err := s.hashConfined(source.Area, source.Key)
	if err != nil {
		return PromotionResult{}, err
	}
	if expectedSHA256 != "" && sum != expectedSHA256 {
		return PromotionResult{}, fmt.Errorf("source hash mismatch: got %s want %s", sum, expectedSHA256)
	}

	if err := s.mkdirParents(dstRoot, destination.Key); err != nil {
		return PromotionResult{}, err
	}

	// Try same-filesystem atomic no-replace rename.
	err = unix.Renameat2(srcRoot, source.Key, dstRoot, destination.Key, unix.RENAME_NOREPLACE)
	if err == nil {
		return PromotionResult{SHA256: sum, Size: size}, nil
	}
	if errors.Is(err, unix.EEXIST) {
		return PromotionResult{}, fmt.Errorf("destination already exists")
	}
	if !errors.Is(err, unix.EXDEV) {
		return PromotionResult{}, err
	}

	// Cross-device: copy to an exclusive temp beside the destination, sync,
	// then rename no-replace.
	tmpKey := destination.Key + ".tmp-" + randHexOrDie(8)
	if err := s.copyConfined(ctx, source, StorageRef{Area: destination.Area, Key: tmpKey}); err != nil {
		s.Remove(destination.Area, tmpKey)
		return PromotionResult{}, err
	}
	if err := unix.Renameat2(dstRoot, tmpKey, dstRoot, destination.Key, unix.RENAME_NOREPLACE); err != nil {
		s.Remove(destination.Area, tmpKey)
		if errors.Is(err, unix.EEXIST) {
			return PromotionResult{}, fmt.Errorf("destination already exists")
		}
		return PromotionResult{}, err
	}
	s.Remove(source.Area, source.Key)
	return PromotionResult{CrossDevice: true, SHA256: sum, Size: size}, nil
}

func (s *ConfinedStorage) hashConfined(area StorageArea, key string) (string, int64, error) {
	f, err := s.OpenRead(area, key)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func (s *ConfinedStorage) copyConfined(ctx context.Context, source, dest StorageRef) error {
	in, err := s.OpenRead(source.Area, source.Key)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := s.CreateExclusive(dest.Area, dest.Key, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, &ctxReader{ctx: ctx, r: in}); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// ctxReader cancels a copy when ctx is done.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

func randHexOrDie(n int) string {
	tok, err := secureToken(n)
	if err != nil {
		// secureToken only fails on entropy failure; that must abort the op.
		panic("storage: entropy failure generating staging key")
	}
	return tok
}
