package httpx

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

func TestGinAdapterPreservesPathsStreamingAndHEAD(t *testing.T) {
	router := New(CORS([]string{"*"}, "Content-Type", "GET, HEAD, OPTIONS"))
	Handle(router, "GET", "/files/{id}/{path...}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: %s:%s\n\n", r.PathValue("id"), r.PathValue("path"))
		w.(http.Flusher).Flush()
	})
	for _, method := range []string{"GET", "HEAD"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(method, "/files/thread/mnt/output/report.txt", nil))
		if response.Code != 200 || !response.Flushed || !strings.Contains(response.Body.String(), "thread:mnt/output/report.txt") {
			t.Fatalf("%s: %d %s", method, response.Code, response.Body.String())
		}
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("OPTIONS", "/files/thread/a", nil))
	if response.Code != 204 || response.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal(response)
	}
}
func TestRateLimitIsSharedAcrossEnginesAndExpires(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	limiter, err := RateLimit("test", client, 1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	engines := []http.Handler{New(limiter), New(limiter)}
	for _, engine := range engines {
		Handle(engine.(*gin.Engine), "GET", "/run", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	}
	for i, want := range []int{200, 429} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest("GET", "/run", nil)
		request.RemoteAddr = "127.0.0.1:1234"
		engines[i].ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		if want == 429 && response.Header().Get("Retry-After") == "" {
			t.Fatal("missing retry hint")
		}
	}
	server.FastForward(time.Minute)
	response := httptest.NewRecorder()
	engines[0].ServeHTTP(response, httptest.NewRequest("GET", "/run", nil))
	if response.Code != 200 {
		t.Fatal(response)
	}
}
