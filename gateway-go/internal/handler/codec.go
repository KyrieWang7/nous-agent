package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

func decode(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func problem(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"detail": err.Error()})
}

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

func sse(w http.ResponseWriter, event string, id int64, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if id > 0 {
		if _, err = fmt.Fprintf(w, "id: %d\n", id); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, raw)
	return err
}
