package files

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestArtifactRejectsTraversal(t *testing.T) {
	manager := New(filepath.Join(t.TempDir(), "workspaces"), filepath.Join(t.TempDir(), "skills"))
	if _, err := manager.Artifact("thread-1", "/mnt/user-data/../../secret"); err == nil {
		t.Fatal("traversal was accepted")
	}
	if _, err := manager.Artifact("../thread", "/mnt/user-data/outputs/a.txt"); err == nil {
		t.Fatal("unsafe thread ID was accepted")
	}
}

func TestInstallSkillExtractsValidatedArchive(t *testing.T) {
	root := t.TempDir()
	manager := New(filepath.Join(root, "workspaces"), filepath.Join(root, "skills"))
	archivePath, err := manager.Artifact("thread-1", "/mnt/user-data/outputs/writer.skill")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(archivePath), 0o750); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	entry, err := writer.Create("writer/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("---\nname: writer\ndescription: test\n---\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	name, err := manager.InstallSkill("thread-1", "/mnt/user-data/outputs/writer.skill")
	if err != nil {
		t.Fatal(err)
	}
	if name != "writer" {
		t.Fatalf("name=%q", name)
	}
	if _, err := os.Stat(filepath.Join(root, "skills", "custom", "writer", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}
