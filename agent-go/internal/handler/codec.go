package handler

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/KyrieWang7/nous-agent/agent-go/internal/model"
	"github.com/KyrieWang7/nous-agent/agent-go/internal/service"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
)

func queryInt(r *http.Request, key string, fallback, min, max int) int {
	value, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil {
		return fallback
	}
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func queryInt64(r *http.Request, key string, fallback int64) int64 {
	value, err := strconv.ParseInt(r.URL.Query().Get(key), 10, 64)
	if err != nil {
		return fallback
	}
	return value
}

func inputFromMessages(input map[string]any) (string, []message.ContentBlock, error) {
	raw, ok := input["messages"].([]any)
	if !ok {
		return "", nil, nil
	}
	for i := len(raw) - 1; i >= 0; i-- {
		m, ok := raw[i].(map[string]any)
		if !ok {
			continue
		}
		typ, _ := m["type"].(string)
		role, _ := m["role"].(string)
		if typ == "human" || typ == "user" || role == "user" {
			return parseUserContent(m["content"])
		}
	}
	return "", nil, nil
}

func parseUserContent(content any) (string, []message.ContentBlock, error) {
	if text, ok := content.(string); ok {
		return text, nil, nil
	}

	parts, ok := content.([]any)
	if !ok {
		return "", nil, fmt.Errorf("user message content must be a string or an array")
	}
	texts := make([]string, 0, len(parts))
	blocks := make([]message.ContentBlock, 0, len(parts))
	for index, rawPart := range parts {
		part, ok := rawPart.(map[string]any)
		if !ok {
			return "", nil, fmt.Errorf("content block %d must be an object", index)
		}
		partType, _ := part["type"].(string)
		switch partType {
		case "text":
			text, ok := part["text"].(string)
			if !ok {
				return "", nil, fmt.Errorf("text block %d requires text", index)
			}
			texts = append(texts, text)
		case "image_url":
			imageURL, err := imageURLFromPart(part["image_url"])
			if err != nil {
				return "", nil, fmt.Errorf("image block %d: %w", index, err)
			}
			block, err := imageBlockFromDataURL(imageURL)
			if err != nil {
				return "", nil, fmt.Errorf("image block %d: %w", index, err)
			}
			blocks = append(blocks, block)
		default:
			return "", nil, fmt.Errorf("content block %d has unsupported type %q", index, partType)
		}
	}
	return strings.Join(texts, "\n"), blocks, nil
}

func imageURLFromPart(value any) (string, error) {
	switch image := value.(type) {
	case string:
		return image, nil
	case map[string]any:
		url, ok := image["url"].(string)
		if !ok || url == "" {
			return "", errors.New("image_url requires url")
		}
		return url, nil
	default:
		return "", errors.New("image_url must be a string or an object")
	}
}

func imageBlockFromDataURL(value string) (message.ContentBlock, error) {
	metadata, encoded, ok := strings.Cut(strings.TrimPrefix(value, "data:"), ",")
	if !strings.HasPrefix(value, "data:") || !ok {
		return message.ContentBlock{}, errors.New("only data URLs are supported")
	}
	segments := strings.Split(metadata, ";")
	mimeType := segments[0]
	if !strings.HasPrefix(mimeType, "image/") {
		return message.ContentBlock{}, fmt.Errorf("unsupported media type %q", mimeType)
	}
	if len(segments) < 2 || segments[len(segments)-1] != "base64" {
		return message.ContentBlock{}, errors.New("image data URL must be base64 encoded")
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return message.ContentBlock{}, fmt.Errorf("invalid base64 data: %w", err)
	}
	return message.ContentBlock{Type: "image", MimeType: mimeType, Data: base64.StdEncoding.EncodeToString(decoded)}, nil
}

func wireMessages(msgs []message.Message) []wireMessage {
	out := make([]wireMessage, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, toWireMessage(m))
	}
	return out
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	defer func() { _ = r.Body.Close() }()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		writeError(w, 400, err)
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("request body must contain one JSON value")
		}
		writeError(w, 400, err)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"detail": err.Error()})
}

func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, service.ErrThreadBusy) {
		writeError(w, 409, err)
		return
	}
	if errors.Is(err, model.ErrThreadNotFound) || errors.Is(err, model.ErrRunNotFound) {
		writeError(w, 404, err)
		return
	}
	writeError(w, 500, err)
}
