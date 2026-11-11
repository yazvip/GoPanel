package files

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"os"
	"path"
	"sort"
	"strings"
)

var (
	ErrNotFound     = errors.New("path not found")
	ErrExists       = errors.New("path already exists")
	ErrIsDir        = errors.New("path is a directory")
	ErrNotDir       = errors.New("path is not a directory")
	ErrTooLarge     = errors.New("file is too large")
	ErrConflict     = errors.New("file changed since it was opened")
	ErrBinary       = errors.New("file appears to be binary")
	ErrForbidden    = errors.New("operation is not allowed on this path")
	ErrInvalidRoot  = errors.New("unknown file manager root")
	ErrInvalidName  = errors.New("invalid file name")
	ErrDepth        = errors.New("directory tree is too deep")
	ErrSymlink      = errors.New("symbolic links are not followed")
	ErrReadOnlyView = errors.New("operation not permitted")
)

const (
	defaultMaxEdit = 2 << 20
	maxListEntries = 5000
	maxSearchHits  = 500
	maxSearchDepth = 12
)

type Root struct {
	Token string `json:"token"`
	Label string `json:"label"`
	Path  string `json:"path"`
}

type Service struct {
	roots      []Root
	MaxUpload  int64
	MaxEdit    int64
	MaxSearch  int
	SearchSkip []string
}

type Entry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"is_dir"`
	IsLink  bool   `json:"is_link"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`
	ModeOct string `json:"mode_oct"`
	ModTime int64  `json:"mtime"`
	Mime    string `json:"mime,omitempty"`
}

type File struct {
	Reader    io.ReadCloser
	Info      os.FileInfo
	Name      string
	Mime      string
	IsDir     bool
	Directory string
	root      *os.Root
}

func New(roots []Root, maxUpload int64) *Service {
	if maxUpload <= 0 {
		maxUpload = 1 << 30
	}
	return &Service{
		roots:     roots,
		MaxUpload: maxUpload,
		MaxEdit:   defaultMaxEdit,
		MaxSearch: maxSearchHits,
		SearchSkip: []string{
			".git", "node_modules", ".cache", ".npm", "__pycache__", ".venv", "vendor",
		},
	}
}

func (s *Service) Roots() []Root {
	out := make([]Root, len(s.roots))
	copy(out, s.roots)
	return out
}

func (s *Service) Root(token string) (Root, error) {
	for _, r := range s.roots {
		if r.Token == token {
			return r, nil
		}
	}
	return Root{}, ErrInvalidRoot
}

func (s *Service) open(token, rel string) (*os.Root, string, error) {
	r, err := s.Root(token)
	if err != nil {
		return nil, "", err
	}
	clean, err := cleanRel(rel)
	if err != nil {
		return nil, "", err
	}
	root, err := os.OpenRoot(r.Path)
	if err != nil {
		return nil, "", fmt.Errorf("open root %q: %w", r.Label, err)
	}
	return root, clean, nil
}

func cleanRel(rel string) (string, error) {
	if strings.ContainsRune(rel, 0) {
		return "", ErrInvalidName
	}
	rel = strings.ReplaceAll(rel, "\\", "/")
	clean := path.Clean("/" + rel)
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" {
		clean = "."
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", ErrForbidden
	}
	return clean, nil
}

func cleanName(name string) (string, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsRune(name, 0) {
		return "", ErrInvalidName
	}
	if strings.ContainsAny(name, "/\\") {
		return "", ErrInvalidName
	}
	return name, nil
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	}
	if errors.Is(err, fs.ErrExist) {
		return ErrExists
	}
	return err
}

func entryFromInfo(base, name string, info os.FileInfo) Entry {
	full := name
	if base != "." && base != "" {
		full = path.Join(base, name)
	}
	e := Entry{
		Name:    info.Name(),
		Path:    full,
		IsDir:   info.IsDir(),
		IsLink:  info.Mode()&os.ModeSymlink != 0,
		Size:    info.Size(),
		Mode:    info.Mode().String(),
		ModeOct: fmt.Sprintf("%04o", info.Mode().Perm()),
		ModTime: info.ModTime().Unix(),
	}
	if !e.IsDir && !e.IsLink {
		e.Mime = mime.TypeByExtension(strings.ToLower(path.Ext(info.Name())))
	}
	return e
}

func (s *Service) List(token, rel string) ([]Entry, error) {
	root, clean, err := s.open(token, rel)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Stat(clean)
	if err != nil {
		return nil, mapError(err)
	}
	if !info.IsDir() {
		return nil, ErrNotDir
	}
	dir, err := root.Open(clean)
	if err != nil {
		return nil, mapError(err)
	}
	defer dir.Close()
	names, err := dir.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	if len(names) > maxListEntries {
		names = names[:maxListEntries]
	}
	out := make([]Entry, 0, len(names))
	for _, name := range names {
		full := name
		if clean != "." {
			full = path.Join(clean, name)
		}
		fi, err := root.Lstat(full)
		if err != nil {
			continue
		}
		e := entryFromInfo(clean, name, fi)
		if fi.Mode()&os.ModeSymlink != 0 {
			if target, err := root.Stat(full); err == nil {
				e.Size = target.Size()
				e.IsDir = target.IsDir()
				if !e.IsDir {
					e.Mime = mime.TypeByExtension(strings.ToLower(path.Ext(name)))
				}
			}
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func (s *Service) Stat(token, rel string) (Entry, error) {
	root, clean, err := s.open(token, rel)
	if err != nil {
		return Entry{}, err
	}
	defer root.Close()
	info, err := root.Lstat(clean)
	if err != nil {
		return Entry{}, mapError(err)
	}
	name := path.Base(clean)
	if clean == "." {
		name = "."
	}
	e := entryFromInfo(path.Dir(clean), name, info)
	e.Path = clean
	return e, nil
}

func (s *Service) Read(token, rel string) (string, int64, error) {
	root, clean, err := s.open(token, rel)
	if err != nil {
		return "", 0, err
	}
	defer root.Close()
	info, err := root.Stat(clean)
	if err != nil {
		return "", 0, mapError(err)
	}
	if info.IsDir() {
		return "", 0, ErrIsDir
	}
	f, err := root.Open(clean)
	if err != nil {
		return "", 0, mapError(err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, s.MaxEdit+1))
	if err != nil {
		return "", 0, err
	}
	if int64(len(data)) > s.MaxEdit {
		return "", 0, ErrTooLarge
	}
	if looksBinary(data) {
		return "", 0, ErrBinary
	}
	return string(data), info.ModTime().Unix(), nil
}

func looksBinary(data []byte) bool {
	limit := len(data)
	if limit > 8000 {
		limit = 8000
	}
	for i := 0; i < limit; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}

func (s *Service) Write(token, rel, content string, expectedMtime int64) (int64, error) {
	if int64(len(content)) > s.MaxEdit {
		return 0, ErrTooLarge
	}
	root, clean, err := s.open(token, rel)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	if clean == "." {
		return 0, ErrIsDir
	}
	mode := os.FileMode(0o644)
	if info, err := root.Stat(clean); err == nil {
		if info.IsDir() {
			return 0, ErrIsDir
		}
		if expectedMtime > 0 && info.ModTime().Unix() != expectedMtime {
			return 0, ErrConflict
		}
		mode = info.Mode().Perm()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return 0, err
	}
	if err := writeAtomic(root, clean, []byte(content), mode); err != nil {
		return 0, err
	}
	info, err := root.Stat(clean)
	if err != nil {
		return 0, err
	}
	return info.ModTime().Unix(), nil
}

func writeAtomic(root *os.Root, name string, data []byte, mode os.FileMode) error {
	dir := path.Dir(name)
	if dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := path.Join(dir, ".gopanel-tmp-"+randomSuffix())
	if dir == "." {
		tmp = ".gopanel-tmp-" + randomSuffix()
	}
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	if werr == nil {
		werr = f.Sync()
	}
	cerr := f.Close()
	if werr != nil {
		root.Remove(tmp)
		return werr
	}
	if cerr != nil {
		root.Remove(tmp)
		return cerr
	}
	if err := root.Chmod(tmp, mode.Perm()); err != nil {
		root.Remove(tmp)
		return err
	}
	if err := root.Rename(tmp, name); err != nil {
		// Windows refuses to rename over an existing file. The target platform
		// is Linux, but development on Windows should still work; move the old
		// file aside first so a failed swap can be rolled back.
		backup := tmp + ".old"
		if rerr := root.Rename(name, backup); rerr == nil {
			if rerr2 := root.Rename(tmp, name); rerr2 != nil {
				root.Rename(backup, name)
				root.Remove(tmp)
				return err
			}
			root.Remove(backup)
			return nil
		}
		root.Remove(tmp)
		return err
	}
	return nil
}

func (s *Service) Upload(token, dir, name string, r io.Reader, overwrite bool) (Entry, error) {
	name, err := cleanName(name)
	if err != nil {
		return Entry{}, err
	}
	root, cleanDir, err := s.open(token, dir)
	if err != nil {
		return Entry{}, err
	}
	defer root.Close()
	info, err := root.Stat(cleanDir)
	if err != nil {
		return Entry{}, mapError(err)
	}
	if !info.IsDir() {
		return Entry{}, ErrNotDir
	}
	target := name
	if cleanDir != "." {
		target = path.Join(cleanDir, name)
	}
	if _, err := root.Lstat(target); err == nil {
		if !overwrite {
			return Entry{}, ErrExists
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Entry{}, err
	}
	tmp := path.Join(cleanDir, ".gopanel-upload-"+randomSuffix())
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return Entry{}, err
	}
	written, copyErr := io.Copy(f, io.LimitReader(r, s.MaxUpload+1))
	if copyErr == nil && written > s.MaxUpload {
		copyErr = ErrTooLarge
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
		return Entry{}, copyErr
	}
	if err := root.Chmod(tmp, 0o644); err != nil {
		root.Remove(tmp)
		return Entry{}, err
	}
	if err := root.Rename(tmp, target); err != nil {
		root.Remove(tmp)
		return Entry{}, mapError(err)
	}
	fi, err := root.Lstat(target)
	if err != nil {
		return Entry{}, err
	}
	return entryFromInfo(cleanDir, name, fi), nil
}

func (s *Service) Mkdir(token, rel string) (Entry, error) {
	root, clean, err := s.open(token, rel)
	if err != nil {
		return Entry{}, err
	}
	defer root.Close()
	if clean == "." {
		return Entry{}, ErrExists
	}
	if err := root.Mkdir(clean, 0o755); err != nil {
		return Entry{}, mapError(err)
	}
	info, err := root.Lstat(clean)
	if err != nil {
		return Entry{}, err
	}
	return entryFromInfo(path.Dir(clean), path.Base(clean), info), nil
}

func (s *Service) CreateFile(token, rel string) (Entry, error) {
	root, clean, err := s.open(token, rel)
	if err != nil {
		return Entry{}, err
	}
	defer root.Close()
	if clean == "." {
		return Entry{}, ErrInvalidName
	}
	f, err := root.OpenFile(clean, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return Entry{}, mapError(err)
	}
	if err := f.Close(); err != nil {
		return Entry{}, err
	}
	info, err := root.Lstat(clean)
	if err != nil {
		return Entry{}, err
	}
	return entryFromInfo(path.Dir(clean), path.Base(clean), info), nil
}

func (s *Service) Rename(token, from, to string) error {
	root, src, err := s.open(token, from)
	if err != nil {
		return err
	}
	defer root.Close()
	if src == "." {
		return ErrForbidden
	}
	dst, err := cleanRel(to)
	if err != nil {
		return err
	}
	if dst == "." {
		return ErrForbidden
	}
	if err := root.Rename(src, dst); err != nil {
		return mapError(err)
	}
	return nil
}

func (s *Service) Move(token, from, destDir string) error {
	root, src, err := s.open(token, from)
	if err != nil {
		return err
	}
	defer root.Close()
	if src == "." {
		return ErrForbidden
	}
	dstDir, err := cleanRel(destDir)
	if err != nil {
		return err
	}
	info, err := root.Stat(dstDir)
	if err != nil {
		return mapError(err)
	}
	if !info.IsDir() {
		return ErrNotDir
	}
	dst := path.Join(dstDir, path.Base(src))
	if dst == src {
		return nil
	}
	if dstDir != "." {
		if rel, err := relPath(dstDir, src); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
			return ErrForbidden
		}
	}
	if _, err := root.Lstat(dst); err == nil {
		return ErrExists
	}
	if err := root.Rename(src, dst); err != nil {
		return mapError(err)
	}
	return nil
}

func relPath(base, target string) (string, error) {
	base = strings.TrimSuffix(base, "/")
	if base == "." || base == "" {
		return target, nil
	}
	if target == base {
		return ".", nil
	}
	if strings.HasPrefix(target, base+"/") {
		return strings.TrimPrefix(target, base+"/"), nil
	}
	return "..", nil
}

func (s *Service) Copy(token, from, destDir string) (string, error) {
	root, src, err := s.open(token, from)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if src == "." {
		return "", ErrForbidden
	}
	dstDir, err := cleanRel(destDir)
	if err != nil {
		return "", err
	}
	info, err := root.Stat(dstDir)
	if err != nil {
		return "", mapError(err)
	}
	if !info.IsDir() {
		return "", ErrNotDir
	}
	if rel, err := relPath(dstDir, src); err == nil && rel != ".." {
		return "", ErrForbidden
	}
	srcInfo, err := root.Lstat(src)
	if err != nil {
		return "", mapError(err)
	}
	if srcInfo.Mode()&os.ModeSymlink != 0 {
		return "", ErrSymlink
	}
	name := path.Base(src)
	dst := name
	if dstDir != "." {
		dst = path.Join(dstDir, name)
	}
	if _, err := root.Lstat(dst); err == nil {
		return "", ErrExists
	}
	if srcInfo.IsDir() {
		if err := copyTree(root, src, dst, 0); err != nil {
			return "", err
		}
	} else {
		if err := copyFile(root, src, dst, srcInfo.Mode().Perm()); err != nil {
			return "", err
		}
	}
	return dst, nil
}

func copyTree(root *os.Root, src, dst string, depth int) error {
	if depth > 64 {
		return ErrDepth
	}
	info, err := root.Lstat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return copyFile(root, src, dst, info.Mode().Perm())
	}
	if err := root.Mkdir(dst, info.Mode().Perm()&0o777); err != nil {
		return err
	}
	dir, err := root.Open(src)
	if err != nil {
		return err
	}
	names, err := dir.Readdirnames(-1)
	dir.Close()
	if err != nil {
		return err
	}
	for _, name := range names {
		childInfo, err := root.Lstat(path.Join(src, name))
		if err != nil {
			return err
		}
		if childInfo.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if err := copyTree(root, path.Join(src, name), path.Join(dst, name), depth+1); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(root *os.Root, src, dst string, mode os.FileMode) error {
	in, err := root.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := root.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	if copyErr == nil {
		copyErr = out.Sync()
	}
	closeErr := out.Close()
	if copyErr != nil {
		root.Remove(dst)
		return copyErr
	}
	if closeErr != nil {
		root.Remove(dst)
		return closeErr
	}
	return root.Chmod(dst, mode.Perm())
}

func (s *Service) Delete(token, rel string) error {
	root, clean, err := s.open(token, rel)
	if err != nil {
		return err
	}
	defer root.Close()
	if clean == "." {
		return ErrForbidden
	}
	info, err := root.Lstat(clean)
	if err != nil {
		return mapError(err)
	}
	if info.IsDir() {
		return mapError(root.RemoveAll(clean))
	}
	return mapError(root.Remove(clean))
}

func (s *Service) Chmod(token, rel, mode string) error {
	root, clean, err := s.open(token, rel)
	if err != nil {
		return err
	}
	defer root.Close()
	if clean == "." {
		return ErrForbidden
	}
	info, err := root.Lstat(clean)
	if err != nil {
		return mapError(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ErrSymlink
	}
	m, err := parseMode(mode)
	if err != nil {
		return err
	}
	return root.Chmod(clean, m)
}

func parseMode(mode string) (os.FileMode, error) {
	mode = strings.TrimPrefix(strings.TrimSpace(mode), "0o")
	if len(mode) < 3 || len(mode) > 4 {
		return 0, fmt.Errorf("invalid mode")
	}
	var m uint32
	for _, c := range mode {
		if c < '0' || c > '7' {
			return 0, fmt.Errorf("invalid mode")
		}
		m = m*8 + uint32(c-'0')
	}
	if m > 0o777 {
		return 0, fmt.Errorf("mode must be between 000 and 777")
	}
	return os.FileMode(m), nil
}

func (s *Service) OpenRead(token, rel string) (*File, error) {
	root, clean, err := s.open(token, rel)
	if err != nil {
		return nil, err
	}
	info, err := root.Stat(clean)
	if err != nil {
		root.Close()
		return nil, mapError(err)
	}
	if info.IsDir() {
		dir, err := root.Open(clean)
		if err != nil {
			root.Close()
			return nil, mapError(err)
		}
		return &File{Reader: dir, Info: info, Name: path.Base(clean), IsDir: true, Directory: clean, root: root}, nil
	}
	f, err := root.Open(clean)
	if err != nil {
		root.Close()
		return nil, mapError(err)
	}
	return &File{
		Reader: f,
		Info:   info,
		Name:   path.Base(clean),
		Mime:   mime.TypeByExtension(strings.ToLower(path.Ext(clean))),
		root:   root,
	}, nil
}

// Root exposes the sandbox the file was opened in so callers can stream a
// directory archive. The root is closed by Close.
func (f *File) Root() *os.Root { return f.root }

func (f *File) Close() error {
	err := f.Reader.Close()
	if f.root != nil {
		if rerr := f.root.Close(); err == nil {
			err = rerr
		}
	}
	return err
}

// Search matches names case-insensitively. query is a plain substring unless it
// contains glob metacharacters, in which case path.Match is used.
func (s *Service) Search(token, rel, query string) ([]Entry, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil, ErrInvalidName
	}
	root, clean, err := s.open(token, rel)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Stat(clean)
	if err != nil {
		return nil, mapError(err)
	}
	if !info.IsDir() {
		return nil, ErrNotDir
	}
	limit := s.MaxSearch
	if limit <= 0 {
		limit = maxSearchHits
	}
	var out []Entry
	var walk func(dir string, depth int) error
	walk = func(dir string, depth int) error {
		if depth > maxSearchDepth || len(out) >= limit {
			return nil
		}
		d, err := root.Open(dir)
		if err != nil {
			return nil
		}
		names, err := d.Readdirnames(-1)
		d.Close()
		if err != nil {
			return nil
		}
		for _, name := range names {
			if len(out) >= limit {
				return nil
			}
			full := name
			if dir != "." {
				full = path.Join(dir, name)
			}
			fi, err := root.Lstat(full)
			if err != nil {
				continue
			}
			if fi.IsDir() && s.skipSearchDir(name) {
				continue
			}
			if matchName(query, name) {
				out = append(out, entryFromInfo(dir, name, fi))
			}
			if fi.IsDir() && fi.Mode()&os.ModeSymlink == 0 {
				if err := walk(full, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(clean, 0); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) skipSearchDir(name string) bool {
	for _, skip := range s.SearchSkip {
		if name == skip {
			return true
		}
	}
	return false
}

func matchName(query, name string) bool {
	lower := strings.ToLower(name)
	if strings.ContainsAny(query, "*?[") {
		if ok, _ := path.Match(query, lower); ok {
			return true
		}
	}
	return strings.Contains(lower, query)
}

func randomSuffix() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", os.Getpid())
	}
	return hex.EncodeToString(b[:])
}
