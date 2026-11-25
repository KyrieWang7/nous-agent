package handler

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestModelConfigurationRoundTrip(t *testing.T) {
	server, root := testServer(t)
	secret := `local-secret-$literal-"quoted"`
	request := func(method, body string, want int) map[string]any {
		t.Helper()
		r := httptest.NewRequest(method, "/api/models/config", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s status=%d body=%s", method, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "local-secret") || strings.Contains(w.Body.String(), `"api_key":`) {
			t.Fatal("response disclosed model credentials")
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("model settings must not be cached")
		}
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	request("GET", "", 200)
	payload, _ := json.Marshal(map[string]any{"default_model": "custom", "models": []any{map[string]any{
		"name": "custom", "provider": "openai-compatible", "model": "model-v1", "base_url": "https://models.example.test/v1",
		"api_key": secret, "context_length": 128000, "max_tokens": 8192,
	}}})
	created := request("PUT", string(payload), 200)
	if created["default_model"] != "custom" || !created["models"].([]any)[0].(map[string]any)["api_key_configured"].(bool) {
		t.Fatalf("unexpected model settings: %v", created)
	}
	path := filepath.Join(root, "models.json")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("settings permissions: %v, %v", info, err)
	}
	request("PUT", `{"default_model":"custom","models":[{"name":"custom","provider":"openai-compatible","model":"model-v2"}]}`, 200)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var persisted struct {
		Models []struct {
			APIKey string `json:"api_key"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &persisted); err != nil || persisted.Models[0].APIKey != secret {
		t.Fatal("omitting api_key must preserve the existing credential exactly")
	}
	// A settings editor can PUT a redacted GET response without losing its key.
	readback := request("GET", "", 200)
	roundTrip, _ := json.Marshal(readback)
	request("PUT", string(roundTrip), 200)
	list := httptest.NewRecorder()
	server.ServeHTTP(list, httptest.NewRequest("GET", "/api/models", nil))
	if list.Code != 200 || !strings.Contains(list.Body.String(), "custom") || strings.Contains(list.Body.String(), "test-model") {
		t.Fatalf("catalog did not use saved settings: %s", list.Body.String())
	}
	request("PUT", `{"models":[{"name":"custom","provider":"openai-compatible","model":"model-v2","api_key":""}]}`, 200)
	cleared := request("GET", "", 200)
	if cleared["default_model"] != "custom" {
		t.Fatal("an omitted default must select the first configured model")
	}
	if cleared["models"].([]any)[0].(map[string]any)["api_key_configured"].(bool) {
		t.Fatal("explicit empty api_key must clear the stored key")
	}
}

func TestInvalidModelConfigurationIsNotPersisted(t *testing.T) {
	server, root := testServer(t)
	for _, body := range []string{
		`{"models":[]}`,
		`{"models":[{"name":"default","provider":"openai-compatible","model":"x"}]}`,
		`{"models":[{"name":"config","provider":"openai-compatible","model":"x"}]}`,
		`{"models":[{"name":"x","provider":"invalid","model":"x"}]}`,
		`{"models":[{"name":"x","provider":"openai-compatible","model":"x","base_url":"file:///tmp/key"}]}`,
		`{"models":[{"name":"x","provider":"openai-compatible","model":"x","max_tokens":-1}]}`,
		`{"models":[{"name":"x","provider":"deepseek","model":"x","context_length":128000}]}`,
		`{"models":[{"name":"x","provider":"anthropic","model":"x","context_length":4096}]}`,
		`{"models":[{"name":"x","provider":"openai-compatible","model":"x","max_tokens":256000}]}`,
		`{"default_model":"missing","models":[{"name":"x","provider":"openai-compatible","model":"x"}]}`,
		`{"models":[{"name":"x","provider":"openai-compatible","model":"x"},{"name":"x","provider":"openai-compatible","model":"x"}]}`,
		`{"models":[{"name":"x","provider":"deepseek","model":"x","extra_body":{"a":1}}]}`,
		`{"models":[{"name":"x","provider":"openai-compatible","model":"x","typo":true}]}`,
		`{"models":[{"name":"x","provider":"openai-compatible","model":"x"}]} {}`,
	} {
		w := httptest.NewRecorder()
		server.ServeHTTP(w, httptest.NewRequest("PUT", "/api/models/config", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatalf("expected validation failure, got %d: %s", w.Code, w.Body.String())
		}
		if _, err := os.Stat(filepath.Join(root, "models.json")); !os.IsNotExist(err) {
			t.Fatalf("invalid configuration changed persisted settings: %v", err)
		}
	}
}
