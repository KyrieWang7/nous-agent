package files

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	domain "github.com/KyrieWang7/nous-agent/gateway-go/internal/model"
)

const virtualWorkspace = "/mnt/user-data/"

func (m *Manager) openWorkspace(threadID string) (*os.Root, error) {
	base, err := m.threadPath(threadID, ".")
	if err != nil {
		return nil, err
	}
	parent, err := os.OpenRoot(filepath.Dir(base))
	if err != nil {
		return nil, err
	}
	defer func() { _ = parent.Close() }()
	stat, err := parent.Lstat(filepath.Base(base))
	if err != nil {
		return nil, err
	}
	if !stat.IsDir() {
		return nil, fs.ErrPermission
	}
	return parent.OpenRoot(filepath.Base(base))
}

func workspacePath(value string) (string, error) {
	if value == "" || value == "." || value == strings.TrimSuffix(virtualWorkspace, "/") || value == virtualWorkspace {
		return ".", nil
	}
	value = strings.TrimPrefix(strings.TrimPrefix(value, "/"), strings.TrimPrefix(virtualWorkspace, "/"))
	if !fs.ValidPath(value) {
		return "", fs.ErrInvalid
	}
	return value, nil
}

func (m *Manager) OpenArtifact(threadID, file string) (*os.File, error) {
	name, err := workspacePath(file)
	if err != nil {
		return nil, err
	}
	root, err := m.openWorkspace(threadID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.Open(name)
}

func (m *Manager) Workspace(ctx context.Context, threadID, dir, query string) (domain.WorkspaceListing, error) {
	out := domain.WorkspaceListing{Entries: []domain.WorkspaceEntry{}}
	dir, err := workspacePath(dir)
	if err != nil {
		return out, err
	}
	root, err := m.openWorkspace(threadID)
	if errors.Is(err, fs.ErrNotExist) && dir == "." {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	defer func() { _ = root.Close() }()
	query = strings.ToLower(strings.TrimSpace(query))
	collect := func(name string, entry fs.DirEntry) {
		out.Entries = append(out.Entries, domain.WorkspaceEntry{
			Name: entry.Name(), Path: virtualWorkspace + name, Directory: entry.IsDir(),
		})
	}
	visible := func(entry fs.DirEntry) bool {
		return !strings.HasPrefix(entry.Name(), ".") && (entry.IsDir() || entry.Type().IsRegular())
	}
	if query == "" {
		entries, err := fs.ReadDir(root.FS(), dir)
		if err != nil {
			return out, err
		}
		for _, entry := range entries {
			if visible(entry) {
				collect(path.Join(dir, entry.Name()), entry)
			}
		}
		return out, nil
	}
	visited := 0
	err = fs.WalkDir(root.FS(), dir, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == dir {
			return nil
		}
		if visited >= 20000 || len(out.Entries) >= 200 {
			out.Truncated = true
			return fs.SkipAll
		}
		visited++
		if !visible(entry) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if strings.Contains(strings.ToLower(name), query) {
			collect(name, entry)
		}
		return nil
	})
	return out, err
}
