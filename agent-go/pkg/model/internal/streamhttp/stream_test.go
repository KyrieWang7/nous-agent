package streamhttp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStreamOutlivesTotalTimeout(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 8; i++ {
			fmt.Fprint(w, "x")
			w.(http.Flusher).Flush()
			time.Sleep(15 * time.Millisecond)
		}
	}))
	defer s.Close()
	req, _ := http.NewRequest("GET", s.URL, nil)
	resp, err := Do(&http.Client{Timeout: 30 * time.Millisecond}, req, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil || len(b) != 8 {
		t.Fatalf("data=%q err=%v", b, err)
	}
}
func TestIdleAndCallerCancellation(t *testing.T) {
	for _, cancelCaller := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelCaller), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, "x")
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer s.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "GET", s.URL, nil)
			resp, err := Do(s.Client(), req, 40*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if cancelCaller {
				cancel()
			}
			_, err = io.ReadAll(resp.Body)
			want := ErrIdleTimeout
			if cancelCaller {
				want = context.Canceled
			}
			if !errors.Is(err, want) {
				t.Fatalf("err=%v want=%v", err, want)
			}
		})
	}
}
