package archive

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"
)

var (
	ErrUnsafe     = errors.New("archive contains an unsafe entry")
	ErrTooLarge   = errors.New("archive exceeds the extraction limits")
	ErrTooMany    = errors.New("archive contains too many files")
	ErrBadArchive = errors.New("invalid archive")
)

type Limits struct {
	MaxFiles      int
	MaxTotalBytes int64
	MaxFileBytes  int64
	MaxDepth      int
}

func DefaultLimits() Limits {
	return Limits{
		MaxFiles:      20000,
		MaxTotalBytes: 2 << 30,
		MaxFileBytes:  1 << 30,
		MaxDepth:      64,
	}
}

func (l Limits) normalize() Limits {
	d := DefaultLimits()
	if l.MaxFiles <= 0 {
		l.MaxFiles = d.MaxFiles
	}
	if l.MaxTotalBytes <= 0 {
		l.MaxTotalBytes = d.MaxTotalBytes
	}
	if l.MaxFileBytes <= 0 {
		l.MaxFileBytes = d.MaxFileBytes
	}
	if l.MaxDepth <= 0 {
		l.MaxDepth = d.MaxDepth
	}
	return l
}

func safeName(name string) (string, error) {
	if strings.ContainsRune(name, 0) {
		return "", ErrUnsafe
	}
	name = strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(name, "/") {
		return "", ErrUnsafe
	}
	clean := path.Clean(name)
	if clean == "." || clean == "" {
		return "", nil
	}
	if clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
		return "", ErrUnsafe
	}
	if len(clean) >= 2 && clean[1] == ':' {
		return "", ErrUnsafe
	}
	if len(strings.Split(clean, "/")) > 64 {
		return "", ErrUnsafe
	}
	return clean, nil
}

func destDir(dest string) (string, error) {
	dest = strings.TrimSpace(strings.ReplaceAll(dest, "\\", "/"))
	if dest == "" || dest == "." || dest == "/" {
		return ".", nil
	}
	if strings.HasPrefix(dest, "/") {
		return "", ErrUnsafe
	}
	clean := path.Clean(dest)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", ErrUnsafe
	}
	if strings.ContainsRune(clean, 0) {
		return "", ErrUnsafe
	}
	return clean, nil
}

func joinUnder(root *os.Root, dest, name string) (string, error) {
	clean, err := safeName(name)
	if err != nil {
		return "", err
	}
	if clean == "" {
		return "", nil
	}
	full := clean
	if dest != "." && dest != "" {
		full = path.Join(dest, clean)
	}
	return full, nil
}

func ensureParent(root *os.Root, name string) error {
	dir := path.Dir(name)
	if dir == "." {
		return nil
	}
	return root.MkdirAll(dir, 0o755)
}

func writeFile(root *os.Root, name string, r io.Reader, size int64, mode os.FileMode, modTime time.Time) error {
	if err := ensureParent(root, name); err != nil {
		return err
	}
	if mode == 0 {
		mode = 0o644
	}
	dir := path.Dir(name)
	tmp := ".gopanel-extract-" + fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid())
	if dir != "." {
		tmp = path.Join(dir, tmp)
	}
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(f, io.LimitReader(r, size+1))
	if copyErr == nil && written != size {
		copyErr = ErrBadArchive
	}
	if copyErr == nil {
		copyErr = f.Sync()
	}
	closeErr := f.Close()
	if copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		root.Remove(tmp)
		return copyErr
	}
	if err := root.Chmod(tmp, mode.Perm()&0o777); err != nil {
		root.Remove(tmp)
		return err
	}
	if err := root.Rename(tmp, name); err != nil {
		root.Remove(tmp)
		return err
	}
	if !modTime.IsZero() {
		root.Chtimes(name, modTime, modTime)
	}
	return nil
}

func ExtractTarGz(r io.Reader, root *os.Root, dest string, limits Limits) error {
	limits = limits.normalize()
	cleanDest, err := destDir(dest)
	if err != nil {
		return err
	}
	gz, err := gzip.NewReader(bufio.NewReaderSize(r, 64<<10))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBadArchive, err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var files int
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("%w: %v", ErrBadArchive, err)
		}
		if hdr.Typeflag == tar.TypeXGlobalHeader || hdr.Typeflag == tar.TypeXHeader {
			continue
		}
		name, err := joinUnder(root, cleanDest, hdr.Name)
		if err != nil {
			return err
		}
		if name == "" {
			continue
		}
		files++
		if files > limits.MaxFiles {
			return ErrTooMany
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(name, os.FileMode(hdr.Mode).Perm()&0o777); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if hdr.Size < 0 || hdr.Size > limits.MaxFileBytes {
				return ErrTooLarge
			}
			total += hdr.Size
			if total > limits.MaxTotalBytes {
				return ErrTooLarge
			}
			if err := writeFile(root, name, tr, hdr.Size, os.FileMode(hdr.Mode), hdr.ModTime); err != nil {
				return err
			}
		case tar.TypeSymlink, tar.TypeLink, tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
			return fmt.Errorf("%w: links and device files are not allowed", ErrUnsafe)
		default:
			// Skip metadata entries such as GNU long names that the tar reader
			// has already applied.
			continue
		}
	}
	return nil
}

func ExtractZipFile(filename string, root *os.Root, dest string, limits Limits) error {
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	return ExtractZipReaderAt(f, info.Size(), root, dest, limits)
}

func ExtractZipReaderAt(r io.ReaderAt, size int64, root *os.Root, dest string, limits Limits) error {
	limits = limits.normalize()
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBadArchive, err)
	}
	cleanDest, err := destDir(dest)
	if err != nil {
		return err
	}
	var files int
	var total int64
	for _, zf := range zr.File {
		name, err := joinUnder(root, cleanDest, zf.Name)
		if err != nil {
			return err
		}
		if name == "" {
			continue
		}
		mode := zf.Mode()
		if mode&os.ModeSymlink != 0 || mode&os.ModeDevice != 0 || mode&os.ModeNamedPipe != 0 {
			return fmt.Errorf("%w: links and device files are not allowed", ErrUnsafe)
		}
		files++
		if files > limits.MaxFiles {
			return ErrTooMany
		}
		if zf.FileInfo().IsDir() {
			if err := root.MkdirAll(name, mode.Perm()&0o777); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			continue
		}
		if int64(zf.UncompressedSize64) > limits.MaxFileBytes {
			return ErrTooLarge
		}
		total += int64(zf.UncompressedSize64)
		if total > limits.MaxTotalBytes {
			return ErrTooLarge
		}
		rc, err := zf.Open()
		if err != nil {
			return fmt.Errorf("%w: %v", ErrBadArchive, err)
		}
		err = writeFile(root, name, rc, int64(zf.UncompressedSize64), mode, zf.Modified)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func walk(root *os.Root, base string, fn func(rel string, info os.FileInfo) error) error {
	var walkDir func(dir, rel string, depth int) error
	walkDir = func(dir, rel string, depth int) error {
		if depth > 64 {
			return ErrUnsafe
		}
		d, err := root.Open(dir)
		if err != nil {
			return err
		}
		names, err := d.Readdirnames(-1)
		d.Close()
		if err != nil {
			return err
		}
		for _, name := range names {
			full := name
			if dir != "." {
				full = path.Join(dir, name)
			}
			childRel := name
			if rel != "" {
				childRel = path.Join(rel, name)
			}
			info, err := root.Lstat(full)
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			if err := fn(childRel, info); err != nil {
				return err
			}
			if info.IsDir() {
				if err := walkDir(full, childRel, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walkDir(base, "", 0)
}

func WriteTarGz(w io.Writer, root *os.Root, base string) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	writeErr := walk(root, base, func(rel string, info os.FileInfo) error {
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel
		if info.IsDir() {
			hdr.Name += "/"
		}
		hdr.Uid, hdr.Gid = 0, 0
		hdr.Uname, hdr.Gname = "", ""
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		f, err := root.Open(path.Join(base, rel))
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, f)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
	if writeErr != nil {
		tw.Close()
		gz.Close()
		return writeErr
	}
	if err := tw.Close(); err != nil {
		gz.Close()
		return err
	}
	return gz.Close()
}

func WriteZip(w io.Writer, root *os.Root, base string) error {
	zw := zip.NewWriter(w)
	writeErr := walk(root, base, func(rel string, info os.FileInfo) error {
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		hdr.Name = rel
		if info.IsDir() {
			hdr.Name += "/"
			hdr.Method = zip.Store
		} else {
			hdr.Method = zip.Deflate
		}
		dst, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		f, err := root.Open(path.Join(base, rel))
		if err != nil {
			return err
		}
		_, err = io.Copy(dst, f)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
	if writeErr != nil {
		zw.Close()
		return writeErr
	}
	return zw.Close()
}
