package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KyrieWang7/nous-agent/gateway-go/internal/catalog"
	"github.com/KyrieWang7/nous-agent/gateway-go/internal/files"
	"github.com/KyrieWang7/nous-agent/gateway-go/internal/store"
	"github.com/xuri/excelize/v2"
)

func TestFrontendControlPlaneContract(t *testing.T) {
	server, root := testServer(t)
	assertJSON(t, server, "GET", "/api/models", nil, http.StatusOK, func(body map[string]any) {
		if len(body["models"].([]any)) != 1 {
			t.Fatal(body)
		}
	})
	assertJSON(t, server, "GET", "/api/skills", nil, http.StatusOK, func(body map[string]any) {
		if len(body["skills"].([]any)) != 1 {
			t.Fatal(body)
		}
	})
	assertJSON(t, server, "GET", "/api/mcp/config", nil, http.StatusOK, func(body map[string]any) {
		if body["mcp_servers"] == nil {
			t.Fatal(body)
		}
	})
	assertJSON(t, server, "GET", "/api/threads", nil, http.StatusOK, func(body map[string]any) {
		if body["total"].(float64) != 0 {
			t.Fatal(body)
		}
	})
	assertJSON(t, server, "GET", "/api/memory", nil, http.StatusOK, func(body map[string]any) {
		if body["version"] != "1.0" {
			t.Fatal(body)
		}
	})
	if _, err := os.Stat(filepath.Join(root, "skills", "public", "writer", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestUploadAndArtifactRoundTrip(t *testing.T) {
	server, _ := testServer(t)
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("files", "notes.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/threads/thread-1/uploads", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	server.ServeHTTP(response, req)
	if response.Code != 200 {
		t.Fatalf("upload: %d %s", response.Code, response.Body.String())
	}
	req = httptest.NewRequest("GET", "/api/threads/thread-1/artifacts/mnt/user-data/uploads/notes.txt", nil)
	response = httptest.NewRecorder()
	server.ServeHTTP(response, req)
	if response.Code != 200 || response.Body.String() != "hello" {
		t.Fatalf("artifact: %d %q", response.Code, response.Body.String())
	}
}

func TestUploadConvertsSpreadsheetToMarkdown(t *testing.T) {
	server, _ := testServer(t)
	workbook := excelize.NewFile()
	_ = workbook.SetCellValue("Sheet1", "A1", "Name")
	_ = workbook.SetCellValue("Sheet1", "B1", "Value")
	_ = workbook.SetCellValue("Sheet1", "A2", "alpha")
	_ = workbook.SetCellValue("Sheet1", "B2", 42)
	data, err := workbook.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	_ = workbook.Close()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("files", "table.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/api/threads/thread-1/uploads", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", response.Code, response.Body.String())
	}
	var result struct {
		Files []struct {
			MarkdownFile        string `json:"markdown_file"`
			MarkdownArtifactURL string `json:"markdown_artifact_url"`
		} `json:"files"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || result.Files[0].MarkdownFile != "table.md" {
		t.Fatalf("response=%s", response.Body.String())
	}
	artifact := httptest.NewRecorder()
	server.ServeHTTP(artifact, httptest.NewRequest("GET", result.Files[0].MarkdownArtifactURL, nil))
	if artifact.Code != http.StatusOK || !strings.Contains(artifact.Body.String(), "| Name | Value |") {
		t.Fatalf("artifact: %d %s", artifact.Code, artifact.Body.String())
	}
}

func testServer(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	write := func(path, value string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "config.yaml"), "models:\n  - name: test-model\n")
	write(filepath.Join(root, "skills", "public", "writer", "SKILL.md"), "---\nname: writer\ndescription: Writes clearly\n---\n")
	database, err := store.Open(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	return New(catalog.New(filepath.Join(root, "config.yaml"), filepath.Join(root, "extensions.json"), filepath.Join(root, "skills")), files.New(filepath.Join(root, "workspaces"), filepath.Join(root, "skills")), database, []string{"*"}), root
}

func assertJSON(t *testing.T, server http.Handler, method, path string, body []byte, status int, assert func(map[string]any)) {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != status {
		t.Fatalf("%s: %d %s", path, response.Code, response.Body.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	assert(decoded)
}
