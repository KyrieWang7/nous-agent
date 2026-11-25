package middleware

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

const creativeSessionCookie = "open_ai_canvas_session"
const CreativePrefix = "/creative"

type WebUpstreams struct {
	FrontendURL    string
	CreativeURL    string
	CreativeWebURL string
	PublicOrigin   string
}

type CreativeWeb struct {
	web          http.Handler
	publicOrigin string
	api          *url.URL
	client       *http.Client
}

func NewWebUpstreams(cfg WebUpstreams) (http.Handler, *CreativeWeb, error) {
	frontend, err := upstream(cfg.FrontendURL)
	if err != nil {
		return nil, nil, fmt.Errorf("frontend: %w", err)
	}
	var frontendHandler http.Handler
	if frontend != nil {
		frontendHandler = &httputil.ReverseProxy{Rewrite: func(req *httputil.ProxyRequest) {
			req.SetURL(frontend)
			req.SetXForwarded()
			cookies := req.Out.Cookies()
			req.Out.Header.Del("Cookie")
			for _, cookie := range cookies {
				if cookie.Name != creativeSessionCookie {
					req.Out.AddCookie(cookie)
				}
			}
		}}
	}
	if cfg.CreativeURL == "" && cfg.CreativeWebURL == "" {
		return frontendHandler, nil, nil
	}
	creative, err := upstream(cfg.CreativeURL)
	if err != nil || creative == nil {
		return nil, nil, errors.New("CREATIVE_API_URL must be an HTTP(S) origin")
	}
	web, err := upstream(cfg.CreativeWebURL)
	if err != nil || web == nil {
		return nil, nil, errors.New("CREATIVE_WEB_URL must be an HTTP(S) origin")
	}
	public, err := upstream(cfg.PublicOrigin)
	if err != nil || public == nil {
		return nil, nil, errors.New("PUBLIC_WEB_ORIGIN must be an HTTP(S) origin")
	}
	public.Path = ""
	creativeProxy := &httputil.ReverseProxy{
		Rewrite: func(req *httputil.ProxyRequest) {
			req.SetURL(web)
			req.Out.Host = public.Host
			req.SetXForwarded()
			req.Out.Header.Set("X-Forwarded-Host", public.Host)
			req.Out.Header.Set("X-Forwarded-Proto", public.Scheme)
		},
		ModifyResponse: func(response *http.Response) error {
			if response.StatusCode < 300 || response.StatusCode >= 400 {
				return nil
			}
			location, err := url.Parse(response.Header.Get("Location"))
			if err != nil || location.Path == "" || !strings.HasPrefix(location.Path, "/") {
				return nil
			}
			if location.Host != "" &&
				(!strings.EqualFold(location.Host, public.Host) || location.Scheme != public.Scheme) {
				return nil
			}
			if location.Path != CreativePrefix && !strings.HasPrefix(location.Path, CreativePrefix+"/") {
				if location.RawPath != "" {
					location.RawPath = CreativePrefix + location.RawPath
				}
				location.Path = CreativePrefix + location.Path
				response.Header.Set("Location", location.String())
			}
			return nil
		},
	}
	return frontendHandler, &CreativeWeb{
		web: http.StripPrefix(CreativePrefix, creativeProxy), publicOrigin: public.String(), api: creative,
		client: &http.Client{
			Timeout:       8 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
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

func (c *CreativeWeb) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == CreativePrefix+"/api/auth/nous-session" {
		http.NotFound(w, r)
		return
	}
	if !c.validateOrigin(r) {
		http.Error(w, "Same-origin request required", http.StatusForbidden)
		return
	}
	c.web.ServeHTTP(w, r)
}

func (c *CreativeWeb) Login(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	query.Set("next", "/nous/return")
	http.Redirect(w, r, CreativePrefix+r.URL.Path+"?"+query.Encode(), http.StatusSeeOther)
}

func (c *CreativeWeb) Session(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	user, err := c.session(r)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"enabled": true, "user": user})
}

func (c *CreativeWeb) Logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !c.validateOrigin(r) {
		http.Error(w, "Same-origin request required", http.StatusForbidden)
		return
	}
	response, err := c.request(r, http.MethodPost, "/api/auth/logout")
	if err != nil {
		http.Error(w, "Authentication service unavailable", http.StatusServiceUnavailable)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		http.Error(w, "Could not end session", http.StatusBadGateway)
		return
	}
	var result struct {
		Code *int `json:"code"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 32<<10)).Decode(&result); err != nil || result.Code == nil || *result.Code != 0 {
		http.Error(w, "Could not end session", http.StatusBadGateway)
		return
	}
	for _, cookie := range response.Cookies() {
		if cookie.Name == creativeSessionCookie {
			http.SetCookie(w, cookie)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (c *CreativeWeb) AuthorizeWorkspace(w http.ResponseWriter, r *http.Request, isAPI bool) bool {
	if !c.validateOrigin(r) {
		http.Error(w, "Same-origin request required", http.StatusForbidden)
		return false
	}
	user, err := c.session(r)
	if err != nil {
		http.Error(w, "Authentication service unavailable", http.StatusServiceUnavailable)
		return false
	}
	if user == nil {
		if !isAPI && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
		} else {
			http.Error(w, "Sign in required", http.StatusUnauthorized)
		}
		return false
	}
	if user.Role != "admin" {
		http.Error(w, "The Nous Agent workspace requires an administrator account", http.StatusForbidden)
		return false
	}
	return true
}

type creativeUser struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Email       string `json:"email,omitempty"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role"`
	Status      string `json:"status"`
}

func (c *CreativeWeb) session(r *http.Request) (*creativeUser, error) {
	if _, err := r.Cookie(creativeSessionCookie); err != nil {
		return nil, nil
	}
	response, err := c.request(r, http.MethodGet, "/api/auth/nous-session")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return nil, nil
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("session lookup returned HTTP %d", response.StatusCode)
	}
	var result struct {
		Code *int `json:"code"`
		Data struct {
			User *creativeUser `json:"user"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return nil, err
	}
	if result.Code == nil || *result.Code != 0 {
		return nil, errors.New("session lookup failed")
	}
	user := result.Data.User
	if user == nil || user.ID == "" || user.Status != "active" {
		return nil, nil
	}
	return user, nil
}

func (c *CreativeWeb) request(r *http.Request, method, path string) (*http.Response, error) {
	endpoint := *c.api
	endpoint.Path = path
	req, err := http.NewRequestWithContext(r.Context(), method, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	if cookie, err := r.Cookie(creativeSessionCookie); err == nil {
		req.AddCookie(cookie)
	}
	if strings.HasPrefix(c.publicOrigin, "https://") {
		req.Header.Set("X-Forwarded-Proto", "https")
	}
	return c.client.Do(req)
}

func (c *CreativeWeb) validateOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin != "" {
		return origin == c.publicOrigin
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return true
	}
	site := r.Header.Get("Sec-Fetch-Site")
	return site == "" || site == "same-origin"
}
