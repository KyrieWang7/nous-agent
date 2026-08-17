package handlers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
)

const NameUploads = "uploads"

type Uploads struct{}

func NewUploads() *Uploads             { return &Uploads{} }
func (Uploads) Name() string           { return NameUploads }
func (Uploads) Grade() lifecycle.Grade { return lifecycle.GradeListener }
func (Uploads) BeforeAgent(_ context.Context, st *lifecycle.State) error {
	raw, ok := st.Value("thread_data")
	if !ok {
		return nil
	}
	paths, ok := raw.(map[string]string)
	if !ok || paths["uploads_path"] == "" {
		return nil
	}
	entries, err := os.ReadDir(paths["uploads_path"])
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var names []string
	for _, entry := range entries {
		if entry.Type().IsRegular() {
			names = append(names, entry.Name())
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("<uploaded_files>\n")
	for _, name := range names {
		fmt.Fprintf(&b, "- %s\n  Path: %s\n", name, filepath.ToSlash(filepath.Join("/mnt/user-data/uploads", name)))
	}
	b.WriteString("</uploaded_files>")
	st.History.Append(message.Message{Role: message.RoleSystem, Content: b.String()})
	st.SetValue("uploaded_files", names)
	return nil
}
