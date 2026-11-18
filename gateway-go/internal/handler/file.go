package handler

import (
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
)

func (s *Handler) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 256<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		problem(w, 400, err)
		return
	}
	items, err := s.app.SaveUploads(r.Context(), r.PathValue("id"), r.MultipartForm.File["files"])
	if err != nil {
		problem(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "files": items, "message": fmt.Sprintf("Successfully uploaded %d file(s)", len(items))})
}

func (s *Handler) uploads(w http.ResponseWriter, r *http.Request) {
	items, err := s.app.ListUploads(r.PathValue("id"))
	if err != nil {
		problem(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"files": items, "count": len(items)})
}

func (s *Handler) deleteUpload(w http.ResponseWriter, r *http.Request) {
	err := s.app.DeleteUpload(r.PathValue("id"), r.PathValue("name"))
	if errors.Is(err, os.ErrNotExist) {
		problem(w, 404, err)
		return
	}
	if err != nil {
		problem(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "message": "Deleted " + r.PathValue("name")})
}

func (s *Handler) workspaceFiles(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	result, err := s.app.Workspace(r.Context(), r.PathValue("id"), r.URL.Query().Get("path"), r.URL.Query().Get("q"))
	switch {
	case err == nil:
		writeJSON(w, 200, result)
	case errors.Is(err, fs.ErrInvalid):
		problem(w, 400, err)
	case errors.Is(err, fs.ErrNotExist):
		problem(w, 404, err)
	default:
		problem(w, 500, errors.New("workspace is unavailable"))
	}
}

func (s *Handler) artifact(w http.ResponseWriter, r *http.Request) {
	file, err := s.app.OpenArtifact(r.PathValue("id"), r.PathValue("path"))
	if errors.Is(err, os.ErrNotExist) {
		problem(w, 404, errors.New("artifact not found"))
		return
	}
	if err != nil {
		problem(w, 403, errors.New("artifact is unavailable"))
		return
	}
	defer func() { _ = file.Close() }()
	stat, err := file.Stat()
	if err != nil {
		problem(w, 500, err)
		return
	}
	if !stat.Mode().IsRegular() {
		problem(w, 400, errors.New("path is not a file"))
		return
	}
	name := filepath.Base(stat.Name())
	contentType := mime.TypeByExtension(filepath.Ext(name))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if r.URL.Query().Get("download") == "true" {
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	} else {
		w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": name}))
	}
	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, name, stat.ModTime(), file)
}
