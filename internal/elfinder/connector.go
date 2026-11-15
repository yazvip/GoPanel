// Package elfinder serves the elFinder connector protocol on top of the
// panel's file service, so the bundled elFinder UI can drive it.
package elfinder

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"gopanel/internal/files"
)

var (
	// ErrBadTarget is returned when a hash does not point at a file.
	ErrBadTarget = errors.New("invalid target")
	// ErrReadOnly is returned when a write is attempted without permission.
	ErrReadOnly = errors.New("the file manager is read only")
)

// Connector answers the elFinder commands for one panel instance.
type Connector struct {
	files  *files.Service
	roots  []files.Root
	logger *slog.Logger
}

// New builds a connector for the given file roots.
func New(svc *files.Service, roots []files.Root, logger *slog.Logger) *Connector {
	if logger == nil {
		logger = slog.Default()
	}
	return &Connector{files: svc, roots: roots, logger: logger}
}

// Handle runs one elFinder command. allowWrite mirrors the caller's role.
func (c *Connector) Handle(w http.ResponseWriter, r *http.Request, allowWrite bool) {
	// A multipart body is parsed by the upload command itself, so the form
	// values must not be read here.
	cmd := strings.TrimSpace(r.URL.Query().Get("cmd"))
	if !isMultipart(r) {
		cmd = strings.TrimSpace(r.FormValue("cmd"))
	}
	switch cmd {
	case "init":
		c.init(w, allowWrite)
	case "open", "ls":
		c.open(w, r, allowWrite)
	case "tree":
		c.tree(w, r)
	case "parents":
		c.tree(w, r)
	case "mkdir":
		c.mkdir(w, r, allowWrite)
	case "mkfile":
		c.mkfile(w, r, allowWrite)
	case "file":
		c.file(w, r, allowWrite)
	case "rm":
		c.rm(w, r, allowWrite)
	case "rename":
		c.rename(w, r, allowWrite)
	case "paste":
		c.paste(w, r, allowWrite)
	case "duplicate":
		c.duplicate(w, r, allowWrite)
	case "get", "info":
		c.get(w, r, allowWrite)
	case "size":
		c.size(w, r)
	case "search":
		c.search(w, r)
	case "upload":
		c.upload(w, r, allowWrite)
	case "archive":
		c.archive(w, r, allowWrite)
	case "extract":
		c.extract(w, r, allowWrite)
	case "zipDownload":
		c.zipDownload(w, r)
	case "ping":
		c.write(w, map[string]any{"ping": 1})
	case "callback":
		c.write(w, map[string]any{})
	default:
		c.fail(w, "unknown command: "+cmd)
	}
}

func isMultipart(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data")
}

/* ---------------- helpers ---------------- */

// encodeHash packs a volume index and a root relative path into the opaque
// identifier elFinder passes back on every command.
func encodeHash(volume int, rel string) string {
	raw := []byte(strconv.Itoa(volume) + "|" + rel)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeHash(hash string) (int, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(hash)
	if err != nil {
		return 0, "", ErrBadTarget
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return 0, "", ErrBadTarget
	}
	volume, err := strconv.Atoi(parts[0])
	if err != nil || volume < 1 {
		return 0, "", ErrBadTarget
	}
	return volume, parts[1], nil
}

func (c *Connector) volume(index int) (files.Root, bool) {
	if index < 1 || index > len(c.roots) {
		return files.Root{}, false
	}
	return c.roots[index-1], true
}

// resolve turns a hash into a volume and a root relative path.
func (c *Connector) resolve(hash string) (int, files.Root, string, error) {
	if strings.TrimSpace(hash) == "" {
		if len(c.roots) == 0 {
			return 0, files.Root{}, "", ErrBadTarget
		}
		return 1, c.roots[0], "", nil
	}
	volume, rel, err := decodeHash(hash)
	if err != nil {
		return 0, files.Root{}, "", err
	}
	root, ok := c.volume(volume)
	if !ok {
		return 0, files.Root{}, "", ErrBadTarget
	}
	return volume, root, rel, nil
}

func writeFlag(allowWrite bool) int {
	if allowWrite {
		return 1
	}
	return 0
}

func (c *Connector) write(w http.ResponseWriter, payload map[string]any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		c.logger.Debug("elfinder response", "error", err)
	}
}

func (c *Connector) fail(w http.ResponseWriter, message string) {
	c.write(w, map[string]any{"error": []string{message}})
}

// volumeObject describes one exposed root.
func (c *Connector) volumeObject(index int, root files.Root, allowWrite bool) map[string]any {
	return map[string]any{
		"hash":     encodeHash(index, ""),
		"volume":   strconv.Itoa(index),
		"name":     root.Label,
		"mime":     "directory",
		"mimetype": "directory",
		"dir":      true,
		"read":     1,
		"write":    writeFlag(allowWrite),
		"lock":     0,
		"ts":       time.Now().Unix(),
		"size":     0,
	}
}

// object describes one file or folder for elFinder.
func (c *Connector) object(volume int, rel string, entry files.Entry, allowWrite bool) map[string]any {
	mimeType := entry.Mime
	if entry.IsDir {
		mimeType = "directory"
	}
	parent := path.Dir(rel)
	if parent == "." {
		parent = ""
	}
	obj := map[string]any{
		"hash":     encodeHash(volume, rel),
		"volume":   strconv.Itoa(volume),
		"name":     entry.Name,
		"size":     entry.Size,
		"mimetype": mimeType,
		"ts":       entry.ModTime,
		"read":     1,
		"write":    writeFlag(allowWrite),
		"lock":     0,
		"parent":   encodeHash(volume, parent),
	}
	if entry.IsDir {
		obj["dir"] = true
	}
	return obj
}

// dirEntry describes a folder without reading it from disk.
func dirEntry(rel string, root files.Root) files.Entry {
	name := root.Label
	if rel != "" {
		name = path.Base(rel)
	}
	return files.Entry{Name: name, Path: rel, IsDir: true, ModTime: time.Now().Unix(), Mime: "directory"}
}

// ancestors returns the breadcrumb from the volume root down to rel.
func (c *Connector) ancestors(volume int, rel string) []map[string]any {
	root, ok := c.volume(volume)
	if !ok {
		return []map[string]any{}
	}
	out := []map[string]any{{
		"name": root.Label,
		"hash": encodeHash(volume, ""),
	}}
	acc := ""
	for _, part := range strings.Split(rel, "/") {
		if part == "" {
			continue
		}
		acc = joinPath(acc, part)
		out = append(out, map[string]any{
			"name": part,
			"hash": encodeHash(volume, acc),
		})
	}
	return out
}

func joinPath(dir, name string) string {
	if dir == "" || dir == "." {
		return name
	}
	return dir + "/" + name
}

/* ---------------- commands ---------------- */

func (c *Connector) init(w http.ResponseWriter, allowWrite bool) {
	volumes := make([]map[string]any, 0, len(c.roots))
	for i, root := range c.roots {
		volumes = append(volumes, c.volumeObject(i+1, root, allowWrite))
	}
	c.write(w, map[string]any{
		"id":         "gopanel",
		"api":        "2.1",
		"files":      volumes,
		"netDrivers": map[string]any{},
		"options": map[string]any{
			"commands": map[string]any{
				"open": true, "parents": true, "tree": true, "mkdir": true, "mkfile": true,
				"rename": true, "paste": true, "rm": true, "get": true, "archive": true,
				"extract": true, "search": true, "info": true, "size": true, "duplicate": true,
				"upload": true, "ping": true, "callback": true,
				"editor": false, "help": false, "netmount": false, "crop": false, "resize": false, "tmb": false,
			},
			"separators": map[string]any{"/": "1"},
		},
	})
}

func (c *Connector) open(w http.ResponseWriter, r *http.Request, allowWrite bool) {
	volume, root, rel, err := c.resolve(r.FormValue("target"))
	if err != nil {
		c.fail(w, err.Error())
		return
	}
	entries, err := c.files.List(root.Token, rel)
	if err != nil {
		c.fail(w, err.Error())
		return
	}
	list := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		list = append(list, c.object(volume, entry.Path, entry, allowWrite))
	}
	payload := map[string]any{
		"cwd":        c.object(volume, rel, dirEntry(rel, root), allowWrite),
		"files":      list,
		"uplMaxSize": c.files.MaxUpload,
		"uplMaxFile": 20,
	}
	if r.FormValue("tree") == "1" {
		payload["tree"] = c.ancestors(volume, rel)
	}
	c.write(w, payload)
}

func (c *Connector) tree(w http.ResponseWriter, r *http.Request) {
	volume, _, rel, err := c.resolve(r.FormValue("target"))
	if err != nil {
		c.fail(w, err.Error())
		return
	}
	c.write(w, map[string]any{"tree": c.ancestors(volume, rel)})
}

func (c *Connector) mkdir(w http.ResponseWriter, r *http.Request, allowWrite bool) {
	if !allowWrite {
		c.fail(w, ErrReadOnly.Error())
		return
	}
	volume, root, rel, err := c.resolve(r.FormValue("target"))
	if err != nil {
		c.fail(w, err.Error())
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		c.fail(w, "the folder name is missing")
		return
	}
	target := joinPath(rel, name)
	if _, err := c.files.Mkdir(root.Token, target); err != nil {
		c.fail(w, err.Error())
		return
	}
	entry, err := c.files.Stat(root.Token, target)
	if err != nil {
		c.fail(w, err.Error())
		return
	}
	c.write(w, map[string]any{"added": []map[string]any{c.object(volume, entry.Path, entry, allowWrite)}})
}

func (c *Connector) mkfile(w http.ResponseWriter, r *http.Request, allowWrite bool) {
	if !allowWrite {
		c.fail(w, ErrReadOnly.Error())
		return
	}
	volume, root, rel, err := c.resolve(r.FormValue("target"))
	if err != nil {
		c.fail(w, err.Error())
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = firstUploadName(r.Form["upload[]"])
	}
	if name == "" {
		c.fail(w, "the file name is missing")
		return
	}
	target := joinPath(rel, name)
	if _, err := c.files.CreateFile(root.Token, target); err != nil {
		c.fail(w, err.Error())
		return
	}
	entry, err := c.files.Stat(root.Token, target)
	if err != nil {
		c.fail(w, err.Error())
		return
	}
	c.write(w, map[string]any{"added": []map[string]any{c.object(volume, entry.Path, entry, allowWrite)}})
}

// firstUploadName reads the name out of the upload[] payload of mkfile,
// which carries a JSON array of {name, size}.
func firstUploadName(raw []string) string {
	if len(raw) == 0 {
		return ""
	}
	var items []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(raw[0]), &items); err == nil && len(items) > 0 {
		return strings.TrimSpace(items[0].Name)
	}
	return strings.TrimSpace(raw[0])
}

func (c *Connector) file(w http.ResponseWriter, r *http.Request, allowWrite bool) {
	_, root, rel, err := c.resolve(r.FormValue("target"))
	if err != nil {
		c.fail(w, err.Error())
		return
	}
	if r.FormValue("download") == "1" {
		c.download(w, root, rel)
		return
	}
	entry, err := c.files.Stat(root.Token, rel)
	if err != nil {
		c.fail(w, err.Error())
		return
	}
	c.write(w, map[string]any{"files": []map[string]any{c.object(1, entry.Path, entry, allowWrite)}})
}

func (c *Connector) rm(w http.ResponseWriter, r *http.Request, allowWrite bool) {
	if !allowWrite {
		c.fail(w, ErrReadOnly.Error())
		return
	}
	targets := r.Form["targets[]"]
	removed := make([]string, 0, len(targets))
	for _, hash := range targets {
		_, root, rel, err := c.resolve(hash)
		if err != nil {
			c.fail(w, err.Error())
			return
		}
		if err := c.files.Delete(root.Token, rel); err != nil {
			c.fail(w, err.Error())
			return
		}
		removed = append(removed, hash)
	}
	c.write(w, map[string]any{"removed": removed})
}

func (c *Connector) rename(w http.ResponseWriter, r *http.Request, allowWrite bool) {
	if !allowWrite {
		c.fail(w, ErrReadOnly.Error())
		return
	}
	volume, root, rel, err := c.resolve(r.FormValue("target"))
	if err != nil {
		c.fail(w, err.Error())
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		c.fail(w, "the new name is missing")
		return
	}
	target := joinPath(path.Dir(rel), name)
	if err := c.files.Rename(root.Token, rel, target); err != nil {
		c.fail(w, err.Error())
		return
	}
	entry, err := c.files.Stat(root.Token, target)
	if err != nil {
		c.fail(w, err.Error())
		return
	}
	c.write(w, map[string]any{
		"added":   []map[string]any{c.object(volume, entry.Path, entry, allowWrite)},
		"removed": []string{encodeHash(volume, rel)},
	})
}

func (c *Connector) paste(w http.ResponseWriter, r *http.Request, allowWrite bool) {
	if !allowWrite {
		c.fail(w, ErrReadOnly.Error())
		return
	}
	volume, root, dst, err := c.resolve(r.FormValue("dst"))
	if err != nil {
		c.fail(w, err.Error())
		return
	}
	cut := r.FormValue("cut") == "1"
	added := make([]map[string]any, 0, len(r.Form["targets[]"]))
	for _, hash := range r.Form["targets[]"] {
		srcVolume, _, src, err := c.resolve(hash)
		if err != nil {
			c.fail(w, err.Error())
			return
		}
		if srcVolume != volume {
			c.fail(w, "items can only be pasted inside the same volume")
			return
		}
		if cut {
			if err := c.files.Move(root.Token, src, dst); err != nil {
				c.fail(w, err.Error())
				return
			}
		} else if _, err := c.files.Copy(root.Token, src, dst); err != nil {
			c.fail(w, err.Error())
			return
		}
		moved := joinPath(dst, path.Base(src))
		entry, err := c.files.Stat(root.Token, moved)
		if err != nil {
			c.fail(w, err.Error())
			return
		}
		added = append(added, c.object(volume, entry.Path, entry, allowWrite))
	}
	c.write(w, map[string]any{"added": added})
}

func (c *Connector) duplicate(w http.ResponseWriter, r *http.Request, allowWrite bool) {
	if !allowWrite {
		c.fail(w, ErrReadOnly.Error())
		return
	}
	volume, root, rel, err := c.resolve(r.FormValue("target"))
	if err != nil {
		c.fail(w, err.Error())
		return
	}
	dir := path.Dir(rel)
	target := joinPath(dir, c.uniqueName(root, dir, path.Base(rel)))
	if err := c.files.CopyAs(root.Token, rel, target); err != nil {
		c.fail(w, err.Error())
		return
	}
	entry, err := c.files.Stat(root.Token, target)
	if err != nil {
		c.fail(w, err.Error())
		return
	}
	c.write(w, map[string]any{"added": []map[string]any{c.object(volume, entry.Path, entry, allowWrite)}})
}

// uniqueName finds a free name next to the original one.
func (c *Connector) uniqueName(root files.Root, dir, name string) string {
	ext := path.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; i < 1000; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if _, err := c.files.Stat(root.Token, joinPath(dir, candidate)); err != nil {
			return candidate
		}
	}
	return fmt.Sprintf("%s (copy)%s", base, ext)
}

// backupName keeps the original name and appends the connector's suffix, the
// way elFinder asks for a backup copy.
func (c *Connector) backupName(root files.Root, dir, name, suffix string) string {
	if suffix == "" {
		suffix = "_"
	}
	ext := path.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; i < 1000; i++ {
		candidate := fmt.Sprintf("%s%s%d%s", base, suffix, i, ext)
		if _, err := c.files.Stat(root.Token, joinPath(dir, candidate)); err != nil {
			return candidate
		}
	}
	return fmt.Sprintf("%s%s%s", base, suffix, ext)
}

func (c *Connector) get(w http.ResponseWriter, r *http.Request, allowWrite bool) {
	volume, root, rel, err := c.resolve(r.FormValue("target"))
	if err != nil {
		c.fail(w, err.Error())
		return
	}
	entry, err := c.files.Stat(root.Token, rel)
	if err != nil {
		c.fail(w, err.Error())
		return
	}
	c.write(w, map[string]any{"files": []map[string]any{c.object(volume, entry.Path, entry, allowWrite)}})
}

// download streams a single file as an attachment.
func (c *Connector) download(w http.ResponseWriter, root files.Root, rel string) {
	file, err := c.files.OpenRead(root.Token, rel)
	if err != nil {
		c.fail(w, err.Error())
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": file.Name}))
	w.Header().Set("Cache-Control", "no-store")
	if _, err := io.Copy(w, file.Reader); err != nil {
		c.logger.Warn("elfinder download", "path", rel, "error", err)
	}
}

func (c *Connector) size(w http.ResponseWriter, r *http.Request) {
	_, root, rel, err := c.resolve(r.FormValue("target"))
	if err != nil {
		c.fail(w, err.Error())
		return
	}
	entry, err := c.files.Stat(root.Token, rel)
	if err != nil {
		c.fail(w, err.Error())
		return
	}
	c.write(w, map[string]any{"size": entry.Size})
}

func (c *Connector) search(w http.ResponseWriter, r *http.Request) {
	volume, root, rel, err := c.resolve(r.FormValue("target"))
	if err != nil {
		c.fail(w, err.Error())
		return
	}
	query := strings.TrimSpace(r.FormValue("q"))
	if query == "" {
		c.write(w, map[string]any{"files": []map[string]any{}})
		return
	}
	found, err := c.files.Search(root.Token, rel, query)
	if err != nil {
		c.fail(w, err.Error())
		return
	}
	list := make([]map[string]any, 0, len(found))
	for _, entry := range found {
		list = append(list, c.object(volume, entry.Path, entry, true))
	}
	c.write(w, map[string]any{"files": list})
}

// upload stores the files of a multipart request. The parts are read by hand
// so the file names can carry the folder structure of a dropped directory.
func (c *Connector) upload(w http.ResponseWriter, r *http.Request, allowWrite bool) {
	if !allowWrite {
		c.fail(w, ErrReadOnly.Error())
		return
	}
	reader, err := r.MultipartReader()
	if err != nil {
		c.fail(w, err.Error())
		return
	}
	var (
		target   string
		suffix   = "_"
		renames  = map[string]bool{}
		paths    []string
		uploads  []pendingUpload
		firstErr error
	)
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			firstErr = err
			break
		}
		switch part.FormName() {
		case "target", "current":
			value, _ := io.ReadAll(io.LimitReader(part, 4096))
			target = strings.TrimSpace(string(value))
		case "suffix":
			value, _ := io.ReadAll(io.LimitReader(part, 64))
			suffix = strings.TrimSpace(string(value))
		case "renames[]":
			value, _ := io.ReadAll(io.LimitReader(part, 1024))
			renames[strings.TrimSpace(string(value))] = true
		case "upload_path[]":
			value, _ := io.ReadAll(io.LimitReader(part, 4096))
			paths = append(paths, strings.TrimSpace(string(value)))
		case "upload[]":
			name := partFileName(part)
			if name == "" {
				part.Close()
				continue
			}
			tmp, err := os.CreateTemp("", "gopanel-elfinder-*")
			if err != nil {
				part.Close()
				firstErr = err
				break
			}
			if _, err := io.Copy(tmp, part); err != nil {
				tmp.Close()
				os.Remove(tmp.Name())
				part.Close()
				firstErr = err
				break
			}
			tmp.Close()
			part.Close()
			uploads = append(uploads, pendingUpload{name: name, path: tmp.Name()})
		default:
			part.Close()
		}
	}
	for _, upload := range uploads {
		os.Remove(upload.path)
	}
	if firstErr != nil {
		c.fail(w, firstErr.Error())
		return
	}
	if len(uploads) == 0 {
		c.fail(w, "no file was uploaded")
		return
	}
	volume, root, rel, err := c.resolve(target)
	if err != nil {
		c.fail(w, err.Error())
		return
	}
	added := make([]map[string]any, 0, len(uploads))
	for i, upload := range uploads {
		name := upload.name
		if i < len(paths) && paths[i] != "" {
			name = path.Join(strings.TrimPrefix(paths[i], "/"), path.Base(name))
		}
		if renames[name] {
			name = c.backupName(root, rel, path.Base(name), suffix)
		}
		file, err := os.Open(upload.path)
		if err != nil {
			c.fail(w, err.Error())
			return
		}
		entry, err := c.files.Upload(root.Token, rel, name, file, true)
		file.Close()
		os.Remove(upload.path)
		if err != nil {
			c.fail(w, err.Error())
			return
		}
		added = append(added, c.object(volume, entry.Path, entry, allowWrite))
	}
	c.write(w, map[string]any{"added": added})
}

type pendingUpload struct {
	name string
	path string
}

// partFileName reads the file name straight from the part header, because
// multipart strips directories from the name.
func partFileName(part *multipart.Part) string {
	disposition := part.Header.Get("Content-Disposition")
	for _, piece := range strings.Split(disposition, ";") {
		piece = strings.TrimSpace(piece)
		if !strings.HasPrefix(piece, "filename=") {
			continue
		}
		name := strings.Trim(strings.TrimPrefix(piece, "filename="), `"`)
		if idx := strings.LastIndex(name, "\\"); idx >= 0 {
			name = name[idx+1:]
		}
		return name
	}
	return ""
}

func (c *Connector) archive(w http.ResponseWriter, r *http.Request, allowWrite bool) {
	if !allowWrite {
		c.fail(w, ErrReadOnly.Error())
		return
	}
	volume, root, rel, err := c.resolve(r.FormValue("target"))
	if err != nil {
		c.fail(w, err.Error())
		return
	}
	format := "zip"
	if strings.HasSuffix(strings.ToLower(r.FormValue("name")), ".tar.gz") {
		format = "tar.gz"
	}
	entry, err := c.files.Archive(root.Token, rel, format)
	if err != nil {
		c.fail(w, err.Error())
		return
	}
	c.write(w, map[string]any{"added": []map[string]any{c.object(volume, entry.Path, entry, allowWrite)}})
}

func (c *Connector) extract(w http.ResponseWriter, r *http.Request, allowWrite bool) {
	if !allowWrite {
		c.fail(w, ErrReadOnly.Error())
		return
	}
	volume, root, rel, err := c.resolve(r.FormValue("target"))
	if err != nil {
		c.fail(w, err.Error())
		return
	}
	dest := strings.TrimSpace(r.FormValue("name"))
	if dest == "" {
		dest = strings.TrimSuffix(path.Base(rel), path.Ext(rel))
	}
	target := joinPath(path.Dir(rel), dest)
	entry, err := c.files.Extract(root.Token, rel, target)
	if err != nil {
		c.fail(w, err.Error())
		return
	}
	c.write(w, map[string]any{"added": []map[string]any{c.object(volume, entry.Path, entry, allowWrite)}})
}

func (c *Connector) zipDownload(w http.ResponseWriter, r *http.Request) {
	_, root, _, err := c.resolve("")
	if err != nil {
		c.fail(w, err.Error())
		return
	}
	rels := make([]string, 0, len(r.Form["targets[]"]))
	for _, hash := range r.Form["targets[]"] {
		_, _, rel, err := c.resolve(hash)
		if err != nil {
			c.fail(w, err.Error())
			return
		}
		rels = append(rels, rel)
	}
	if len(rels) == 0 {
		c.fail(w, "no file was selected")
		return
	}
	reader, err := c.files.OpenZip(root.Token, rels)
	if err != nil {
		c.fail(w, err.Error())
		return
	}
	defer reader.Close()
	name := "files"
	if len(rels) == 1 {
		name = path.Base(rels[0])
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name + ".zip"}))
	w.Header().Set("Cache-Control", "no-store")
	if _, err := io.Copy(w, reader); err != nil {
		c.logger.Warn("elfinder zip download", "error", err)
	}
}
