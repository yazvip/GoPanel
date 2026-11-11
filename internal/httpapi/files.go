package httpapi

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"gopanel/internal/archive"
	"gopanel/internal/audit"
	"gopanel/internal/files"
)

func (s *Server) handleFileRoots(w http.ResponseWriter, r *http.Request) {
	roots := s.Files.Roots()
	s.ok(w, roots)
}

func (s *Server) fileParams(r *http.Request) (root, rel string) {
	return r.URL.Query().Get("root"), r.URL.Query().Get("path")
}

func (s *Server) handleFileList(w http.ResponseWriter, r *http.Request) {
	root, rel := s.fileParams(r)
	entries, err := s.Files.List(root, rel)
	if err != nil {
		s.writeError(w, err)
		return
	}
	if entries == nil {
		entries = []files.Entry{}
	}
	s.ok(w, map[string]any{"root": root, "path": rel, "entries": entries})
}

func (s *Server) handleFileStat(w http.ResponseWriter, r *http.Request) {
	root, rel := s.fileParams(r)
	entry, err := s.Files.Stat(root, rel)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.ok(w, entry)
}

func (s *Server) handleFileDownload(w http.ResponseWriter, r *http.Request) {
	root, rel := s.fileParams(r)
	file, err := s.Files.OpenRead(root, rel)
	if err != nil {
		s.writeError(w, err)
		return
	}
	defer file.Close()
	name := file.Name
	if name == "" {
		name = "download"
	}
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": name})
	if file.IsDir {
		disposition = mime.FormatMediaType("attachment", map[string]string{"filename": name + ".zip"})
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", disposition)
		w.Header().Set("Cache-Control", "no-store")
		if err := archive.WriteZip(w, file.Root(), file.Directory); err != nil {
			s.logger.Warn("stream directory zip", "path", rel, "error", err)
		}
		return
	}
	w.Header().Set("Content-Disposition", disposition)
	if file.Mime != "" {
		w.Header().Set("Content-Type", file.Mime)
	}
	if rs, ok := file.Reader.(io.ReadSeeker); ok {
		http.ServeContent(w, r, file.Name, file.Info.ModTime(), rs)
		return
	}
	w.Header().Set("Content-Length", strconv.FormatInt(file.Info.Size(), 10))
	_, _ = io.Copy(w, file.Reader)
}

func (s *Server) handleFilePreview(w http.ResponseWriter, r *http.Request) {
	root, rel := s.fileParams(r)
	ext := strings.ToLower(path.Ext(rel))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".ico":
		file, err := s.Files.OpenRead(root, rel)
		if err != nil {
			s.writeError(w, err)
			return
		}
		defer file.Close()
		if file.IsDir {
			s.writeError(w, errf(http.StatusBadRequest, "files.is_dir", "path is a directory"))
			return
		}
		contentType := mime.TypeByExtension(ext)
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "private, max-age=60")
		if rs, ok := file.Reader.(io.ReadSeeker); ok {
			http.ServeContent(w, r, file.Name, file.Info.ModTime(), rs)
			return
		}
		_, _ = io.Copy(w, file.Reader)
	case ".svg":
		s.writeError(w, errf(http.StatusUnsupportedMediaType, "files.preview_unsupported", "SVG previews are disabled"))
	default:
		content, mtime, err := s.Files.Read(root, rel)
		if err != nil {
			s.writeError(w, err)
			return
		}
		s.ok(w, map[string]any{"content": content, "mtime": mtime})
	}
}

func (s *Server) handleFileRead(w http.ResponseWriter, r *http.Request) {
	root, rel := s.fileParams(r)
	content, mtime, err := s.Files.Read(root, rel)
	if err != nil {
		if errors.Is(err, files.ErrBinary) {
			s.writeError(w, errf(http.StatusUnsupportedMediaType, "files.binary", "this file is not valid UTF-8 text"))
			return
		}
		if errors.Is(err, files.ErrTooLarge) {
			s.writeError(w, errf(http.StatusRequestEntityTooLarge, "files.too_large", "file is too large to edit"))
			return
		}
		s.writeError(w, err)
		return
	}
	s.ok(w, map[string]any{"content": content, "mtime": mtime, "path": rel, "root": root})
}

type fileWriteRequest struct {
	Root    string `json:"root"`
	Path    string `json:"path"`
	Content string `json:"content"`
	Mtime   int64  `json:"mtime"`
}

func (s *Server) handleFileWrite(w http.ResponseWriter, r *http.Request) {
	var req fileWriteRequest
	limit := s.Files.MaxEdit*2 + 1<<20
	if limit < 3<<20 {
		limit = 3 << 20
	}
	if err := s.decode(w, r, &req, limit); err != nil {
		s.writeError(w, err)
		return
	}
	mtime, err := s.Files.Write(req.Root, req.Path, req.Content, req.Mtime)
	if err != nil {
		s.auditEvent(r, "files.write", req.Path, err.Error(), audit.ResultFailed)
		s.writeError(w, err)
		return
	}
	s.auditEvent(r, "files.write", req.Path, req.Root, audit.ResultSuccess)
	s.ok(w, map[string]any{"mtime": mtime})
}

func (s *Server) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	maxUpload := s.Files.MaxUpload
	if maxUpload <= 0 {
		maxUpload = 1 << 30
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+(4<<20))
	reader, err := r.MultipartReader()
	if err != nil {
		s.writeError(w, errf(http.StatusBadRequest, "request.invalid_multipart", "expected a multipart upload: %v", err))
		return
	}
	root, dir, overwrite := "", "", false
	var uploaded []files.Entry
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				s.writeError(w, errf(http.StatusRequestEntityTooLarge, "files.too_large", "upload is too large"))
				return
			}
			s.writeError(w, errf(http.StatusBadRequest, "request.invalid_multipart", "%v", err))
			return
		}
		switch part.FormName() {
		case "root":
			value, _ := io.ReadAll(io.LimitReader(part, 256))
			root = strings.TrimSpace(string(value))
			part.Close()
		case "path", "dir":
			value, _ := io.ReadAll(io.LimitReader(part, 4096))
			dir = strings.TrimSpace(string(value))
			part.Close()
		case "overwrite":
			value, _ := io.ReadAll(io.LimitReader(part, 16))
			overwrite = strings.TrimSpace(string(value)) == "true" || strings.TrimSpace(string(value)) == "1"
			part.Close()
		case "file":
			name := part.FileName()
			if name == "" {
				part.Close()
				continue
			}
			entry, err := s.Files.Upload(root, dir, name, part, overwrite)
			part.Close()
			if err != nil {
				s.auditEvent(r, "files.upload", path.Join(dir, name), err.Error(), audit.ResultFailed)
				s.writeError(w, err)
				return
			}
			uploaded = append(uploaded, entry)
		default:
			part.Close()
		}
	}
	if len(uploaded) == 0 {
		s.writeError(w, errf(http.StatusBadRequest, "files.no_file", "no file was uploaded"))
		return
	}
	s.auditEvent(r, "files.upload", dir, fmt.Sprintf("%d file(s)", len(uploaded)), audit.ResultSuccess)
	s.created(w, map[string]any{"entries": uploaded})
}

type pathRequest struct {
	Root string `json:"root"`
	Path string `json:"path"`
}

func (s *Server) handleFileMkdir(w http.ResponseWriter, r *http.Request) {
	var req pathRequest
	if err := s.decode(w, r, &req, 32<<10); err != nil {
		s.writeError(w, err)
		return
	}
	entry, err := s.Files.Mkdir(req.Root, req.Path)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.auditEvent(r, "files.mkdir", req.Path, req.Root, audit.ResultSuccess)
	s.created(w, entry)
}

func (s *Server) handleFileCreate(w http.ResponseWriter, r *http.Request) {
	var req pathRequest
	if err := s.decode(w, r, &req, 32<<10); err != nil {
		s.writeError(w, err)
		return
	}
	entry, err := s.Files.CreateFile(req.Root, req.Path)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.auditEvent(r, "files.create", req.Path, req.Root, audit.ResultSuccess)
	s.created(w, entry)
}

type renameRequest struct {
	Root string `json:"root"`
	From string `json:"from"`
	To   string `json:"to"`
}

func (s *Server) handleFileRename(w http.ResponseWriter, r *http.Request) {
	var req renameRequest
	if err := s.decode(w, r, &req, 32<<10); err != nil {
		s.writeError(w, err)
		return
	}
	if err := s.Files.Rename(req.Root, req.From, req.To); err != nil {
		s.writeError(w, err)
		return
	}
	s.auditEvent(r, "files.rename", req.From, req.To, audit.ResultSuccess)
	s.noContent(w)
}

type transferRequest struct {
	Root string `json:"root"`
	From string `json:"from"`
	Dest string `json:"dest"`
}

func (s *Server) handleFileCopy(w http.ResponseWriter, r *http.Request) {
	var req transferRequest
	if err := s.decode(w, r, &req, 32<<10); err != nil {
		s.writeError(w, err)
		return
	}
	dest, err := s.Files.Copy(req.Root, req.From, req.Dest)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.auditEvent(r, "files.copy", req.From, dest, audit.ResultSuccess)
	s.created(w, map[string]any{"path": dest})
}

func (s *Server) handleFileMove(w http.ResponseWriter, r *http.Request) {
	var req transferRequest
	if err := s.decode(w, r, &req, 32<<10); err != nil {
		s.writeError(w, err)
		return
	}
	if err := s.Files.Move(req.Root, req.From, req.Dest); err != nil {
		s.writeError(w, err)
		return
	}
	s.auditEvent(r, "files.move", req.From, req.Dest, audit.ResultSuccess)
	s.noContent(w)
}

func (s *Server) handleFileDelete(w http.ResponseWriter, r *http.Request) {
	var req pathRequest
	if err := s.decode(w, r, &req, 32<<10); err != nil {
		s.writeError(w, err)
		return
	}
	if err := s.Files.Delete(req.Root, req.Path); err != nil {
		s.writeError(w, err)
		return
	}
	s.auditEvent(r, "files.delete", req.Path, req.Root, audit.ResultSuccess)
	s.noContent(w)
}

type chmodRequest struct {
	Root string `json:"root"`
	Path string `json:"path"`
	Mode string `json:"mode"`
}

func (s *Server) handleFileChmod(w http.ResponseWriter, r *http.Request) {
	var req chmodRequest
	if err := s.decode(w, r, &req, 16<<10); err != nil {
		s.writeError(w, err)
		return
	}
	if err := s.Files.Chmod(req.Root, req.Path, req.Mode); err != nil {
		s.writeError(w, err)
		return
	}
	s.auditEvent(r, "files.chmod", req.Path, req.Mode, audit.ResultSuccess)
	s.noContent(w)
}

type archiveRequest struct {
	Root   string `json:"root"`
	Path   string `json:"path"`
	Format string `json:"format"`
	Dest   string `json:"dest"`
}

func (s *Server) handleFileArchive(w http.ResponseWriter, r *http.Request) {
	var req archiveRequest
	if err := s.decode(w, r, &req, 16<<10); err != nil {
		s.writeError(w, err)
		return
	}
	entry, err := s.Files.Archive(req.Root, req.Path, req.Format)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.auditEvent(r, "files.archive", req.Path, entry.Name, audit.ResultSuccess)
	s.created(w, entry)
}

func (s *Server) handleFileExtract(w http.ResponseWriter, r *http.Request) {
	var req archiveRequest
	if err := s.decode(w, r, &req, 16<<10); err != nil {
		s.writeError(w, err)
		return
	}
	entry, err := s.Files.Extract(req.Root, req.Path, req.Dest)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.auditEvent(r, "files.extract", req.Path, entry.Name, audit.ResultSuccess)
	s.created(w, entry)
}

func (s *Server) handleFileSearch(w http.ResponseWriter, r *http.Request) {
	root, rel := s.fileParams(r)
	query := r.URL.Query().Get("q")
	entries, err := s.Files.Search(root, rel, query)
	if err != nil {
		s.writeError(w, err)
		return
	}
	if entries == nil {
		entries = []files.Entry{}
	}
	s.ok(w, map[string]any{"entries": entries, "query": query})
}
