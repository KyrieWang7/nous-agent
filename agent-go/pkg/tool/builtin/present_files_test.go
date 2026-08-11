package builtin_test

import (
	"context"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox/local"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool/builtin"
)

func TestPresentFilesValidatesOutputArtifacts(t *testing.T) {
	provider := local.NewProvider(local.Options{BaseDir: t.TempDir(), VirtualRoot: "/mnt/user-data"})
	lease := sandbox.NewLease(provider, "thread-1")
	ctx := sandbox.NewContext(context.Background(), lease)
	handle, err := lease.Handle(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := handle.FS().WriteFile(ctx, "/mnt/user-data/outputs/report.md", []byte("report")); err != nil {
		t.Fatal(err)
	}

	result, err := builtin.PresentFiles().Handler(ctx, tool.Call{Args: []byte(`{"filepaths":["/mnt/user-data/outputs/report.md"]}`)})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Artifacts) != 1 {
		t.Fatalf("result = %#v", result)
	}
	if got := result.Artifacts[0]; got.Ref != "/mnt/user-data/outputs/report.md" || got.Size != 6 {
		t.Fatalf("artifact = %#v", got)
	}
}

func TestPresentFilesRejectsNonOutputPaths(t *testing.T) {
	provider := local.NewProvider(local.Options{BaseDir: t.TempDir(), VirtualRoot: "/mnt/user-data"})
	ctx := sandbox.NewContext(context.Background(), sandbox.NewLease(provider, "thread-1"))
	result, err := builtin.PresentFiles().Handler(ctx, tool.Call{Args: []byte(`{"filepaths":["/mnt/user-data/uploads/private.txt"]}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("result = %#v, want tool error", result)
	}
}
