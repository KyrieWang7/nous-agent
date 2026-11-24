package middleware

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
)

type WebUpstreams struct {
	FrontendURL  string
	ManagerURL   string
	ManagerToken string
	PublicOrigin string
}

var extensionOrigin = regexp.MustCompile(`^chrome-extension://[a-p]{32}$`)

func NewWebUpstreams(cfg WebUpstreams) (frontendHandler, managerHandler http.Handler, err error) {
	frontend, err := upstream(cfg.FrontendURL)
	if err != nil {
		return nil, nil, fmt.Errorf("frontend: %w", err)
	}
	manager, err := upstream(cfg.ManagerURL)
	if err != nil {
		return nil, nil, fmt.Errorf("manager: %w", err)
	}
	if (manager == nil) != (cfg.ManagerToken == "") {
		return nil, nil, errors.New("manager URL and gateway token must be configured together")
	}
	if cfg.PublicOrigin != "" {
		origin, err := upstream(cfg.PublicOrigin)
		if err != nil || origin.String() != cfg.PublicOrigin || origin.Path != "" {
			return nil, nil, errors.New("public origin must be an HTTP(S) origin without a path")
		}
	}

	if frontend != nil {
		frontendHandler = reverseProxy(frontend, "")
	}
	if manager != nil {
		proxy := reverseProxy(manager, cfg.ManagerToken)
		managerHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !managerOriginAllowed(r, cfg.PublicOrigin) {
				http.Error(w, "Manager requires a same-origin request", http.StatusForbidden)
				return
			}
			proxy.ServeHTTP(w, r)
		})
	}
	return frontendHandler, managerHandler, nil
}

func upstream(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, nil
	}
	target, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if (target.Scheme != "http" && target.Scheme != "https") || target.Hostname() == "" ||
		(target.Path != "" && target.Path != "/") || target.User != nil ||
		target.RawQuery != "" || target.ForceQuery || target.Fragment != "" {
		return nil, errors.New("expected an HTTP(S) upstream origin")
	}
	return target, nil
}

func managerOriginAllowed(r *http.Request, publicOrigin string) bool {
	origin := r.Header.Get("Origin")
	if r.URL.Path == "/api/clipper" && extensionOrigin.MatchString(origin) {
		return true
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return true
	}
	if origin == "" {
		// A browser with Fetch Metadata must come from the exact origin. Local
		// HTTP clients behind the same authenticated ingress can omit Origin.
		return r.Header.Get("Sec-Fetch-Site") == ""
	}
	if publicOrigin == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		publicOrigin = scheme + "://" + r.Host
	}
	return origin == publicOrigin
}

func reverseProxy(target *url.URL, managerToken string) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(req *httputil.ProxyRequest) {
			req.SetURL(target)
			req.SetXForwarded()
			if managerToken != "" {
				req.Out.Header.Set("X-Nous-Manager-Token", managerToken)
				req.Out.Header.Del("Cookie")
				if req.Out.URL.Path != "/api/clipper" {
					req.Out.Header.Del("Authorization")
				}
			}
		},
		ModifyResponse: func(res *http.Response) error {
			if managerToken == "" {
				return nil
			}
			res.Header.Del("X-Nous-Manager-Token")
			location, err := url.Parse(res.Header.Get("Location"))
			if err == nil && location.Scheme == "" && location.Host == "" && strings.HasPrefix(location.Path, "/") {
				location.Path = "/creative" + location.Path
				if location.RawPath != "" {
					location.RawPath = "/creative" + location.RawPath
				}
				res.Header.Set("Location", location.String())
			}
			if strings.HasPrefix(res.Request.URL.Path, "/api/media/") {
				mediaType, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
				switch mediaType {
				case "text/html", "application/xhtml+xml", "image/svg+xml", "text/xml", "application/xml":
					res.Header.Add("Content-Security-Policy", "sandbox allow-scripts")
				}
				res.Header.Set("X-Content-Type-Options", "nosniff")
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "Upstream service is unavailable", http.StatusBadGateway)
		},
	}
}
