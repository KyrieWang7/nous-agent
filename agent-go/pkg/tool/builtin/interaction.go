package builtin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

const maxImageBytes = 20 << 20

const outputDirectory = "/mnt/user-data/outputs"

func WriteTodos() tool.Definition {
	return tool.Definition{
		Name:        "write_todos",
		Group:       "planning",
		Description: "Replace the current task list with updated items and statuses.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"todos":{"type":"array","items":{"type":"object","properties":{"content":{"type":"string"},"status":{"type":"string","enum":["pending","in_progress","completed"]}},"required":["content","status"]}}},"required":["todos"]}`),
		Metadata:    tool.Metadata{IsAgentState: true},
		Handler: func(_ context.Context, call tool.Call) (*tool.Result, error) {
			var args struct {
				Todos []struct {
					Content string `json:"content"`
					Status  string `json:"status"`
				} `json:"todos"`
			}
			if err := json.Unmarshal(call.Args, &args); err != nil {
				return errResult(err), nil
			}
			raw, _ := json.Marshal(args.Todos)
			return &tool.Result{Content: "Task list updated: " + string(raw)}, nil
		},
	}
}

func ViewImage() tool.Definition {
	return tool.Definition{
		Name:        "view_image",
		Group:       GroupFileRead,
		Description: "Read one PNG, JPEG, or WebP image from the workspace and make it visible to the model.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"image_path":{"type":"string"}},"required":["image_path"]}`),
		Metadata:    tool.Metadata{IsReadOnly: true, IsConcurrencySafe: true, RequiresSandbox: true},
		Handler: func(ctx context.Context, call tool.Call) (*tool.Result, error) {
			var args struct {
				ImagePath string `json:"image_path"`
				Path      string `json:"path"`
			}
			if err := json.Unmarshal(call.Args, &args); err != nil {
				return errResult(err), nil
			}
			if args.ImagePath != "" {
				args.Path = args.ImagePath
			}
			h, err := sandbox.HandleFromContext(ctx)
			if err != nil {
				return nil, err
			}
			mime, ok := imageMIME(filepath.Ext(args.Path))
			if !ok {
				return errResult(fmt.Errorf("view_image: unsupported format %q", filepath.Ext(args.Path))), nil
			}
			data, err := h.FS().ReadFile(ctx, args.Path)
			if err != nil {
				return errResult(err), nil
			}
			if len(data) > maxImageBytes {
				return errResult(fmt.Errorf("view_image: image is %d bytes; limit is %d", len(data), maxImageBytes)), nil
			}
			return &tool.Result{
				Content:       "Successfully read image " + args.Path,
				ContentBlocks: []message.ContentBlock{{Type: "image", MimeType: mime, Data: base64.StdEncoding.EncodeToString(data)}},
			}, nil
		},
	}
}

// PresentFiles validates final workspace artifacts and makes their virtual
// paths explicit in the tool transcript. The frontend renders file cards from
// the corresponding assistant tool call, while gateways serve the same paths
// from the shared per-thread workspace volume.
func PresentFiles() tool.Definition {
	return tool.Definition{
		Name:        "present_files",
		Group:       "interaction",
		Description: "Present final files from /mnt/user-data/outputs to the user for viewing or download.",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "filepaths": {
      "type": "array",
      "items": {"type": "string"},
      "minItems": 1,
      "description": "Absolute virtual paths under /mnt/user-data/outputs."
    }
  },
  "required": ["filepaths"]
}`),
		Metadata: tool.Metadata{IsReadOnly: true, IsConcurrencySafe: true, RequiresSandbox: true},
		Handler: func(ctx context.Context, call tool.Call) (*tool.Result, error) {
			var args struct {
				Filepaths []string `json:"filepaths"`
			}
			if err := json.Unmarshal(call.Args, &args); err != nil {
				return errResult(err), nil
			}
			handle, err := sandbox.HandleFromContext(ctx)
			if err != nil {
				return nil, err
			}
			if len(args.Filepaths) == 0 {
				return errResult(fmt.Errorf("present_files: at least one filepath is required")), nil
			}

			artifacts := make([]tool.Artifact, 0, len(args.Filepaths))
			for _, filepath := range args.Filepaths {
				clean := path.Clean(strings.TrimSpace(filepath))
				if clean == outputDirectory || !strings.HasPrefix(clean, outputDirectory+"/") {
					return errResult(fmt.Errorf("present_files: %q is outside %s", filepath, outputDirectory)), nil
				}
				entry, err := handle.FS().Stat(ctx, clean)
				if err != nil {
					return errResult(fmt.Errorf("present_files: %q: %w", clean, err)), nil
				}
				if entry.IsDir {
					return errResult(fmt.Errorf("present_files: %q is a directory", clean)), nil
				}
				artifacts = append(artifacts, tool.Artifact{Kind: "file", Ref: clean, Size: entry.Size})
			}
			raw, _ := json.Marshal(args.Filepaths)
			return &tool.Result{Content: "Successfully presented files: " + string(raw), Artifacts: artifacts}, nil
		},
	}
}

func imageMIME(ext string) (string, bool) {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return "image/jpeg", true
	case ".png":
		return "image/png", true
	case ".webp":
		return "image/webp", true
	default:
		return "", false
	}
}
