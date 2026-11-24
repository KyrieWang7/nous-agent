// Package easel adapts Easel's remaining HTTP contracts during the native migration.
package easel

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
)

var extensionOrigin = regexp.MustCompile(`^chrome-extension://[a-p]{32}$`)

type Handler struct {
	proxy *httputil.ReverseProxy
}

func New(upstream *url.URL) *Handler {
	return &Handler{proxy: &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(upstream)
			origin := r.In.Header.Get("Origin")
			if origin != "" && !(r.In.URL.Path == "/api/clipper" && extensionOrigin.MatchString(origin)) {
				r.Out.Header.Set("Origin", "http://localhost:"+upstream.Port())
			}
		},
		ModifyResponse: func(res *http.Response) error {
			location, err := url.Parse(res.Header.Get("Location"))
			if err == nil && location.Scheme == upstream.Scheme && location.Host == upstream.Host {
				location.Scheme, location.Host = "", ""
				res.Header.Set("Location", location.String())
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "Easel compatibility runtime is unavailable", http.StatusBadGateway)
		},
	}}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.proxy.ServeHTTP(w, r) }
