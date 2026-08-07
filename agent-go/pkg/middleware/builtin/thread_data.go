package builtin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
)

const NameThreadData = "threadData"

type ThreadData struct {
	baseDir string
	lazy    bool
}

func NewThreadData(baseDir string, lazy bool) *ThreadData {
	return &ThreadData{baseDir: baseDir, lazy: lazy}
}

func (ThreadData) Name() string { return NameThreadData }

func (m *ThreadData) BeforeAgent(_ context.Context, st *middleware.State) error {
	if st.ThreadID == "" {
		return errors.New("thread data: thread ID is required")
	}
	if strings.ContainsAny(st.ThreadID, `/\\`) || st.ThreadID == "." || st.ThreadID == ".." {
		return errors.New("thread data: invalid thread ID")
	}
	base := m.baseDir
	if base == "" {
		base = ".nous-agent/threads"
	}
	root := filepath.Join(base, st.ThreadID, "user-data")
	paths := map[string]string{
		"workspace_path": filepath.Join(root, "workspace"),
		"uploads_path":   filepath.Join(root, "uploads"),
		"outputs_path":   filepath.Join(root, "outputs"),
	}
	if !m.lazy {
		for _, path := range paths {
			if err := os.MkdirAll(path, 0o700); err != nil {
				return err
			}
		}
	}
	st.SetValue("thread_data", paths)
	return nil
}
