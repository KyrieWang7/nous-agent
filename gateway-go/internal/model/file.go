package model

type WorkspaceEntry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Directory bool   `json:"directory"`
}

type WorkspaceListing struct {
	Entries   []WorkspaceEntry `json:"entries"`
	Truncated bool             `json:"truncated"`
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
