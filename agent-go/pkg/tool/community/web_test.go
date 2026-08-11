package community

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestWebSearchNormalizesProviderResponse(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://api.tavily.com/search" || request.Method != http.MethodPost {
			t.Fatalf("request = %s %s", request.Method, request.URL)
		}
		return response(200, `{"results":[{"title":"Result","url":"https://example.test","content":"Snippet"}]}`), nil
	})}
	definition, err := Definition(Options{Name: "web_search", APIKey: "secret", Client: client})
	if err != nil {
		t.Fatal(err)
	}
	result, err := definition.Handler(context.Background(), tool.Call{Args: []byte(`{"query":"nous"}`)})
	if err != nil || result.IsError || !strings.Contains(result.Content, "https://example.test") {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
}

func TestImageSearchAcceptsStringAndObjectImages(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(200, `{"images":["https://one.test/a.jpg",{"url":"https://two.test/b.jpg"}]}`), nil
	})}
	definition, _ := Definition(Options{Name: "image_search", APIKey: "secret", Client: client})
	result, err := definition.Handler(context.Background(), tool.Call{Args: []byte(`{"query":"reference","max_results":2}`)})
	if err != nil || result.IsError || !strings.Contains(result.Content, "https://two.test/b.jpg") {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
}

func TestWebFetchValidatesURLAndUsesJinaReader(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://r.jina.ai/https://example.test/page" {
			t.Fatalf("URL = %s", request.URL)
		}
		return response(200, "# Page\n\nReadable"), nil
	})}
	definition, _ := Definition(Options{Name: "web_fetch", Client: client})
	result, err := definition.Handler(context.Background(), tool.Call{Args: []byte(`{"url":"https://example.test/page"}`)})
	if err != nil || result.IsError || !strings.Contains(result.Content, "Readable") {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	invalid, err := definition.Handler(context.Background(), tool.Call{Args: []byte(`{"url":"file:///etc/passwd"}`)})
	if err != nil || !invalid.IsError {
		t.Fatalf("invalid result = %#v, err = %v", invalid, err)
	}
}

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
