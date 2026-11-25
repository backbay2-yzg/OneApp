package archive

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bodgit/sevenzip"
	"github.com/google/uuid"
	"oneapp/internal/domain"
)

// IsArchive reports whether path has a supported archive extension (.zip or .7z).
func IsArchive(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".zip" || ext == ".7z"
}

// ArchiveEntrySummary describes an entry inspected inside an archive without extracting it.
type ArchiveEntrySummary struct {
	RelPath string
	Size    int64
	IsDir   bool
}

// InspectArchive lists files in a .zip or .7z archive, stripping any common single root folder
// so candidate paths match what Extract + UnwrapSingleRoot will produce.
func InspectArchive(archivePath string) ([]ArchiveEntrySummary, int64, error) {
	ext := strings.ToLower(filepath.Ext(archivePath))
	var raw []ArchiveEntrySummary
	var totalSize int64

	switch ext {
	case ".zip":
		r, err := zip.OpenReader(archivePath)
		if err != nil {
			return nil, 0, fmt.Errorf("open zip: %w", err)
		}
		defer r.Close()
		for _, f := range r.File {
			p := filepath.ToSlash(filepath.Clean(f.Name))
			if isJunkRelPath(p) {
				continue
			}
			isDir := f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/")
			sz := int64(f.UncompressedSize64)
			if !isDir {
				totalSize += sz
			}
			raw = append(raw, ArchiveEntrySummary{RelPath: p, Size: sz, IsDir: isDir})
		}
	case ".7z":
		r, err := sevenzip.OpenReader(archivePath)
		if err != nil {
			return nil, 0, fmt.Errorf("open 7z: %w", err)
		}
		defer r.Close()
		for _, f := range r.File {
			p := filepath.ToSlash(filepath.Clean(f.Name))
			if isJunkRelPath(p) {
				continue
			}
			isDir := f.FileInfo().IsDir()
			sz := int64(f.UncompressedSize)
			if !isDir {
				totalSize += sz
			}
			raw = append(raw, ArchiveEntrySummary{RelPath: p, Size: sz, IsDir: isDir})
		}
	default:
		return nil, 0, fmt.Errorf("unsupported archive format: %s", ext)
	}

	// Simulate up to 3 levels of single-root directory unwrapping.
	for level := 0; level < 3; level++ {
		topSegments := make(map[string]bool)
		hasTopFile := false
		for _, item := range raw {
			p := strings.TrimPrefix(item.RelPath, "./")
			p = strings.Trim(p, "/")
			if p == "" || p == "." {
				continue
			}
			parts := strings.SplitN(p, "/", 2)
			if len(parts) == 1 && !item.IsDir {
				hasTopFile = true
				break
			}
			topSegments[parts[0]] = true
		}
		if hasTopFile || len(topSegments) != 1 {
			break
		}
		var prefix string
		for k := range topSegments {
			prefix = k + "/"
		}
		next := make([]ArchiveEntrySummary, 0, len(raw))
		for _, item := range raw {
			p := strings.TrimPrefix(item.RelPath, "./")
			if p == strings.TrimSuffix(prefix, "/") {
				continue
			}
			trimmed := strings.TrimPrefix(p, prefix)
			if trimmed == "" || trimmed == "." {
				continue
			}
			item.RelPath = trimmed
			next = append(next, item)
		}
		raw = next
	}

	return raw, totalSize, nil
}

// Extract unpacks a .zip or .7z archive into destDir with strict Zip Slip path traversal protection.
func Extract(archivePath, destDir string) error {
	cleanDest, err := filepath.Abs(destDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cleanDest, 0o755); err != nil {
		return err
	}

	ext := strings.ToLower(filepath.Ext(archivePath))
	switch ext {
	case ".zip":
		return extractZip(archivePath, cleanDest)
	case ".7z":
		return extract7z(archivePath, cleanDest)
	default:
		return fmt.Errorf("unsupported archive format: %s", ext)
	}
}

func extractZip(archivePath, cleanDest string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	for _, f := range r.File {
		target, err := safeArchiveTarget(cleanDest, f.Name)
		if err != nil {
			return err
		}
		if target == "" {
			continue
		}
		if f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/") {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		outErr := writeStreamToFile(rc, target, f.Mode())
		_ = rc.Close()
		if outErr != nil {
			return outErr
		}
	}
	return nil
}

func extract7z(archivePath, cleanDest string) error {
	r, err := sevenzip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open 7z: %w", err)
	}
	defer r.Close()

	for _, f := range r.File {
		target, err := safeArchiveTarget(cleanDest, f.Name)
		if err != nil {
			return err
		}
		if target == "" {
			continue
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		outErr := writeStreamToFile(rc, target, f.Mode())
		_ = rc.Close()
		if outErr != nil {
			return outErr
		}
	}
	return nil
}

func safeArchiveTarget(cleanDest, entryName string) (string, error) {
	norm := strings.ReplaceAll(entryName, "\\", "/")
	if strings.HasPrefix(norm, "/") || (len(norm) >= 2 && norm[1] == ':') {
		return "", fmt.Errorf("unsafe absolute path in archive: %q", entryName)
	}
	cleaned := filepath.Clean(filepath.FromSlash(norm))
	if cleaned == "." || cleaned == "" {
		return "", nil
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe path traversal in archive: %q", entryName)
	}
	target := filepath.Join(cleanDest, cleaned)
	if !isPathInside(cleanDest, target) {
		return "", fmt.Errorf("archive entry escapes destination: %q", entryName)
	}
	return target, nil
}

func isPathInside(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func writeStreamToFile(src io.Reader, target string, mode fs.FileMode) error {
	perm := mode.Perm()
	if perm == 0 {
		perm = 0o644
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, src); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// UnwrapSingleRoot promotes contents if dir contains a single redundant wrapper folder (up to 3 levels).
func UnwrapSingleRoot(dir string) error {
	for level := 0; level < 3; level++ {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		var realEntries []os.DirEntry
		var junkPaths []string
		for _, e := range entries {
			if isJunkName(e.Name()) {
				junkPaths = append(junkPaths, filepath.Join(dir, e.Name()))
				continue
			}
			realEntries = append(realEntries, e)
		}
		if len(realEntries) != 1 || !realEntries[0].IsDir() {
			break
		}
		for _, jp := range junkPaths {
			_ = os.RemoveAll(jp)
		}

		singleDir := filepath.Join(dir, realEntries[0].Name())
		// Rename wrapper to a temporary unique name first to avoid collisions if an inner item shares the same name.
		tmpWrapper := filepath.Join(dir, ".__oneapp_unwrap_"+uuid.NewString()[:8])
		if err := os.Rename(singleDir, tmpWrapper); err != nil {
			return err
		}
		innerEntries, err := os.ReadDir(tmpWrapper)
		if err != nil {
			return err
		}
		for _, inner := range innerEntries {
			src := filepath.Join(tmpWrapper, inner.Name())
			dst := filepath.Join(dir, inner.Name())
			if err := os.Rename(src, dst); err != nil {
				return err
			}
		}
		if err := os.RemoveAll(tmpWrapper); err != nil {
			return err
		}
	}
	return nil
}

func isJunkName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	return lower == "__macosx" || lower == ".ds_store" || lower == "thumbs.db" || lower == "desktop.ini"
}

func isJunkRelPath(rel string) bool {
	parts := strings.Split(strings.TrimPrefix(rel, "./"), "/")
	for _, p := range parts {
		if isJunkName(p) {
			return true
		}
	}
	return false
}

// ComputeManifest computes SHA256 hashes and sizes for every file in rootDir.
func ComputeManifest(appID, rootDir string) ([]domain.InstallManifestFile, error) {
	out := make([]domain.InstallManifestFile, 0)
	err := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(rootDir, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		hash, err := hashFileSHA256(path)
		if err != nil {
			return err
		}
		out = append(out, domain.InstallManifestFile{
			AppID:    appID,
			RelPath:  filepath.ToSlash(rel),
			SHA256:   hash,
			FileSize: info.Size(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RelPath < out[j].RelPath })
	return out, nil
}

func hashFileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// CopyDir recursively copies src directory contents into dst.
func CopyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return CopyFile(path, target)
	})
}

// CopyFile copies a single file preserving basic permissions.
func CopyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	perm := fs.FileMode(0o644)
	if err == nil && info.Mode().Perm() != 0 {
		perm = info.Mode().Perm()
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// MovePath moves src to dst, falling back to copy + remove across volumes.
func MovePath(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else {
		info, statErr := os.Stat(src)
		if statErr != nil {
			return statErr
		}
		if info.IsDir() {
			if copyErr := CopyDir(src, dst); copyErr != nil {
				return errors.Join(err, copyErr)
			}
			return os.RemoveAll(src)
		}
		if copyErr := CopyFile(src, dst); copyErr != nil {
			return errors.Join(err, copyErr)
		}
		return os.Remove(src)
	}
}

// DirSize calculates total byte size of all files under root.
func DirSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, infoErr := d.Info(); infoErr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
