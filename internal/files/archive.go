package files

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"

	"gopanel/internal/archive"
)

// Archive compresses rel into a sibling .tar.gz or .zip file and returns the
// new entry. Symlinks inside the tree are skipped by the archive walker.
func (s *Service) Archive(token, rel, format string) (Entry, error) {
	root, clean, err := s.open(token, rel)
	if err != nil {
		return Entry{}, err
	}
	defer root.Close()
	if clean == "." {
		return Entry{}, ErrForbidden
	}
	info, err := root.Lstat(clean)
	if err != nil {
		return Entry{}, mapError(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Entry{}, ErrSymlink
	}
	var ext string
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "zip":
		ext = ".zip"
	case "targz", "tar.gz":
		ext = ".tar.gz"
	default:
		return Entry{}, fmt.Errorf("unsupported archive format")
	}
	base := path.Base(clean)
	dir := path.Dir(clean)
	dest, err := uniqueName(root, dir, base+ext)
	if err != nil {
		return Entry{}, err
	}
	f, err := root.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return Entry{}, mapError(err)
	}
	writeErr := func() error {
		if ext == ".zip" {
			return archive.WriteZip(f, root, clean)
		}
		return archive.WriteTarGz(f, root, clean)
	}()
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		_ = root.Remove(dest)
		if writeErr != nil {
			return Entry{}, writeErr
		}
		return Entry{}, closeErr
	}
	st, err := root.Stat(dest)
	if err != nil {
		return Entry{}, mapError(err)
	}
	return entryFromInfo(dir, path.Base(dest), st), nil
}

// Extract unpacks a .zip or .tar.gz archive into a new directory next to it.
// The destination must not already exist, and extraction is rolled back if any
// entry fails the archive safety checks.
func (s *Service) Extract(token, rel, dest string) (Entry, error) {
	root, clean, err := s.open(token, rel)
	if err != nil {
		return Entry{}, err
	}
	defer root.Close()
	if clean == "." {
		return Entry{}, ErrForbidden
	}
	info, err := root.Lstat(clean)
	if err != nil {
		return Entry{}, mapError(err)
	}
	if info.IsDir() {
		return Entry{}, ErrIsDir
	}
	if !info.Mode().IsRegular() {
		return Entry{}, ErrForbidden
	}
	lower := strings.ToLower(clean)
	var kind string
	switch {
	case strings.HasSuffix(lower, ".zip"):
		kind = "zip"
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		kind = "tar"
	default:
		return Entry{}, fmt.Errorf("unsupported archive format")
	}

	dir := path.Dir(clean)
	if dest == "" {
		name := path.Base(clean)
		for _, suffix := range []string{".tar.gz", ".tgz", ".zip"} {
			if strings.HasSuffix(strings.ToLower(name), suffix) {
				name = name[:len(name)-len(suffix)]
				break
			}
		}
		if name == "" || name == "." || name == ".." {
			name = "extracted"
		}
		dest = name
		dest, err = uniqueName(root, dir, dest)
		if err != nil {
			return Entry{}, err
		}
	} else {
		dest, err = cleanRel(dest)
		if err != nil {
			return Entry{}, err
		}
		if dest == "." {
			return Entry{}, ErrForbidden
		}
		if _, err := root.Lstat(dest); err == nil {
			return Entry{}, ErrExists
		} else if !errors.Is(err, fs.ErrNotExist) {
			return Entry{}, mapError(err)
		}
	}
	if err := root.Mkdir(dest, 0o755); err != nil {
		return Entry{}, mapError(err)
	}

	extractErr := func() error {
		if kind == "tar" {
			f, err := root.Open(clean)
			if err != nil {
				return mapError(err)
			}
			defer f.Close()
			return archive.ExtractTarGz(f, root, dest, archive.DefaultLimits())
		}
		f, err := root.Open(clean)
		if err != nil {
			return mapError(err)
		}
		defer f.Close()
		return archive.ExtractZipReaderAt(f, info.Size(), root, dest, archive.DefaultLimits())
	}()
	if extractErr != nil {
		_ = removeTree(root, dest)
		return Entry{}, extractErr
	}
	st, err := root.Stat(dest)
	if err != nil {
		return Entry{}, mapError(err)
	}
	return entryFromInfo(dir, path.Base(dest), st), nil
}

func uniqueName(root *os.Root, dir, name string) (string, error) {
	for i := 0; i < 100; i++ {
		candidate := name
		if i > 0 {
			ext := ""
			base := name
			if idx := strings.LastIndex(base, ".tar.gz"); idx > 0 {
				ext, base = base[idx:], base[:idx]
			} else if idx := strings.LastIndex(base, "."); idx > 0 {
				ext, base = base[idx:], base[:idx]
			}
			base = fmt.Sprintf("%s-%d", base, i)
			candidate = base + ext
		}
		full := candidate
		if dir != "." {
			full = path.Join(dir, candidate)
		}
		_, err := root.Lstat(full)
		if errors.Is(err, fs.ErrNotExist) {
			return full, nil
		}
		if err != nil {
			return "", mapError(err)
		}
	}
	return "", ErrExists
}

func removeTree(root *os.Root, name string) error {
	info, err := root.Lstat(name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return root.Remove(name)
	}
	d, err := root.Open(name)
	if err != nil {
		return err
	}
	names, err := d.Readdirnames(-1)
	d.Close()
	if err != nil {
		return err
	}
	for _, child := range names {
		if err := removeTree(root, path.Join(name, child)); err != nil {
			return err
		}
	}
	return root.Remove(name)
}
