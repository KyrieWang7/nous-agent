package skill

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeSkill(t *testing.T, root, name, extra, body string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SKILL.md")
	data := "---\nname: " + name + "\ndescription: Use for " + name + " requests\n" + extra + "---\n\n" + body
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestLoadLazyBodyAndHotReload(t *testing.T) {
	root := t.TempDir()
	path := writeSkill(t, root, "reports", "allowed-tools: [read_file]\n", "body-one")
	r, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := r.Get("reports")
	if s.loaded {
		t.Fatal("body loaded during L1 scan")
	}
	body, err := s.Body()
	if err != nil || body != "body-one" {
		t.Fatalf("body=%q err=%v", body, err)
	}
	time.Sleep(10 * time.Millisecond)
	data := "---\nname: reports\ndescription: Use for reports requests\n---\nbody-two"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	body, err = s.Body()
	if err != nil || body != "body-two" {
		t.Fatalf("reloaded body=%q err=%v", body, err)
	}
}
func TestMatchPriorityMutexAndForce(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "low", "priority: 1\nmutex_key: report\n", "low")
	writeSkill(t, root, "high", "priority: 9\nmutex_key: report\n", "high")
	r, _ := Load(root)
	got, err := r.Match(ActivationContext{Prompt: "please handle high and low requests"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "high" {
		t.Fatalf("matches=%v", got)
	}
	forced, err := r.Match(ActivationContext{ForceSkills: []string{"low"}})
	if err != nil || len(forced) != 1 || forced[0].Name != "low" {
		t.Fatalf("forced=%v err=%v", forced, err)
	}
}
func TestNarrowToolsDoesNotExpandPermission(t *testing.T) {
	s := &Skill{Metadata: Metadata{Name: "s", AllowedTools: []string{"read_file", "secret", "deferred"}}}
	allow, disclosed := NarrowTools([]string{"read_file", "deferred"}, []*Skill{s}, map[string]bool{"read_file": true, "deferred": true, "secret": true}, map[string]bool{"deferred": true})
	if len(allow) != 2 || allow[0] != "read_file" || allow[1] != "deferred" {
		t.Fatalf("allow=%v", allow)
	}
	if len(disclosed) != 1 || disclosed[0] != "deferred" {
		t.Fatalf("disclosed=%v", disclosed)
	}
}
func TestLoadRejectsMissingRequiredMetadata(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "bad")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: bad\n---\nbody"), 0o600)
	if _, err := Load(root); err == nil {
		t.Fatal("expected error")
	}
}
