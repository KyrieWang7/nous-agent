package files

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/KyrieWang7/nous-agent/gateway-go/internal/convert"
)

type Manager struct {
	workspaceRoot string
	skillsRoot    string
	converter     *convert.Converter
}

type Info struct {
	Filename            string  `json:"filename"`
	Size                int64   `json:"size"`
	Path                string  `json:"path"`
	VirtualPath         string  `json:"virtual_path"`
	ArtifactURL         string  `json:"artifact_url"`
	Extension           string  `json:"extension,omitempty"`
	Modified            float64 `json:"modified,omitempty"`
	MarkdownFile        string  `json:"markdown_file,omitempty"`
	MarkdownPath        string  `json:"markdown_path,omitempty"`
	MarkdownVirtualPath string  `json:"markdown_virtual_path,omitempty"`
	MarkdownArtifactURL string  `json:"markdown_artifact_url,omitempty"`
}

func New(workspaceRoot, skillsRoot string) *Manager {
	return &Manager{workspaceRoot: workspaceRoot, skillsRoot: skillsRoot, converter: convert.New(os.Getenv("LIBREOFFICE_PATH"), 2*time.Minute)}
}

func (m *Manager) SaveUploads(ctx context.Context, threadID string, headers []*multipart.FileHeader) ([]Info, error) {
	directory, err := m.threadPath(threadID, "uploads")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return nil, err
	}
	files := make([]Info, 0, len(headers))
	for _, header := range headers {
		name := filepath.Base(header.Filename)
		if name == "." || name == ".." || name == "" {
			continue
		}
		source, err := header.Open()
		if err != nil {
			return nil, err
		}
		targetPath := filepath.Join(directory, name)
		target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
		if err != nil {
			source.Close()
			return nil, err
		}
		written, copyErr := io.Copy(target, io.LimitReader(source, 256<<20))
		closeErr := target.Close()
		source.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		info := m.info(threadID, name, written, time.Now())
		if m.converter.Supports(targetPath) {
			markdownPath, conversionErr := m.converter.Convert(ctx, targetPath)
			if conversionErr != nil {
				slog.Warn("document conversion failed", "path", targetPath, "error", conversionErr)
			} else {
				info.MarkdownFile = filepath.Base(markdownPath)
				info.MarkdownPath = markdownPath
				info.MarkdownVirtualPath = "/mnt/user-data/uploads/" + info.MarkdownFile
				info.MarkdownArtifactURL = "/api/threads/" + threadID + "/artifacts" + info.MarkdownVirtualPath
			}
		}
		files = append(files, info)
	}
	return files, nil
}

func (m *Manager) ListUploads(threadID string) ([]Info, error) {
	directory, err := m.threadPath(threadID, "uploads")
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return []Info{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]Info, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		stat, err := entry.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, m.info(threadID, entry.Name(), stat.Size(), stat.ModTime()))
	}
	return out, nil
}

func (m *Manager) DeleteUpload(threadID, filename string) error {
	if filepath.Base(filename) != filename || filename == "." || filename == ".." {
		return errors.New("invalid filename")
	}
	path, err := m.threadPath(threadID, filepath.Join("uploads", filename))
	if err != nil {
		return err
	}
	return os.Remove(path)
}

func (m *Manager) Artifact(threadID, virtualPath string) (string, error) {
	clean := strings.TrimPrefix(strings.TrimPrefix(virtualPath, "/"), "mnt/user-data/")
	if clean == "mnt/user-data" {
		clean = ""
	}
	return m.threadPath(threadID, clean)
}

func (m *Manager) InstallSkill(threadID, virtualPath string) (string, error) {
	if !strings.HasSuffix(strings.ToLower(virtualPath), ".skill") {
		return "", errors.New("file must have .skill extension")
	}
	archive, err := m.Artifact(threadID, virtualPath)
	if err != nil {
		return "", err
	}
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return "", errors.New("file is not a valid ZIP archive")
	}
	defer reader.Close()
	if len(reader.File) == 0 || len(reader.File) > 1000 {
		return "", errors.New("skill archive has an invalid number of files")
	}
	root := ""
	for _, file := range reader.File {
		clean := filepath.ToSlash(filepath.Clean(file.Name))
		if strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") || file.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("skill archive contains an unsafe path")
		}
		parts := strings.Split(clean, "/")
		if len(parts) > 1 && root == "" {
			root = parts[0]
		}
	}
	if root == "" {
		root = strings.TrimSuffix(filepath.Base(archive), filepath.Ext(archive))
	}
	skillName := filepath.Base(root)
	if skillName == "." || skillName == "" {
		return "", errors.New("could not determine skill name")
	}
	target := filepath.Join(m.skillsRoot, "custom", skillName)
	if _, err := os.Stat(target); err == nil {
		return "", fmt.Errorf("skill %q already exists", skillName)
	}
	customRoot := filepath.Join(m.skillsRoot, "custom")
	if err := os.MkdirAll(customRoot, 0o750); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(customRoot, ".installing-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)
	var total int64
	for _, file := range reader.File {
		clean := filepath.Clean(file.Name)
		relative := clean
		if root != "" && strings.HasPrefix(filepath.ToSlash(clean), root+"/") {
			relative = strings.TrimPrefix(filepath.ToSlash(clean), root+"/")
		}
		if relative == "." || relative == "" {
			continue
		}
		destination := filepath.Join(staging, filepath.FromSlash(relative))
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(destination, 0o750); err != nil {
				return "", err
			}
			continue
		}
		total += int64(file.UncompressedSize64)
		if total > 64<<20 {
			return "", errors.New("skill archive is too large")
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
			return "", err
		}
		source, err := file.Open()
		if err != nil {
			return "", err
		}
		targetFile, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
		if err != nil {
			source.Close()
			return "", err
		}
		_, copyErr := io.Copy(targetFile, io.LimitReader(source, 64<<20))
		targetFile.Close()
		source.Close()
		if copyErr != nil {
			return "", copyErr
		}
	}
	if _, err := os.Stat(filepath.Join(staging, "SKILL.md")); err != nil {
		return "", errors.New("skill archive must contain SKILL.md")
	}
	if err := os.Rename(staging, target); err != nil {
		return "", err
	}
	return skillName, nil
}

func (m *Manager) threadPath(threadID, relative string) (string, error) {
	if threadID == "" || filepath.Base(threadID) != threadID || threadID == "." || threadID == ".." {
		return "", errors.New("invalid thread ID")
	}
	root := filepath.Join(m.workspaceRoot, threadID)
	path := filepath.Join(root, filepath.Clean(relative))
	resolvedRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	resolvedPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolvedPath != resolvedRoot && !strings.HasPrefix(resolvedPath, resolvedRoot+string(os.PathSeparator)) {
		return "", errors.New("path traversal is not allowed")
	}
	return resolvedPath, nil
}

func (m *Manager) info(threadID, name string, size int64, modified time.Time) Info {
	virtual := "/mnt/user-data/uploads/" + name
	return Info{Filename: name, Size: size, Path: filepath.Join(m.workspaceRoot, threadID, "uploads", name), VirtualPath: virtual, ArtifactURL: "/api/threads/" + threadID + "/artifacts" + virtual, Extension: filepath.Ext(name), Modified: float64(modified.UnixNano()) / 1e9}
}
