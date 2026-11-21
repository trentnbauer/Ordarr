package app

import (
	"bytes"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// normalizeBasePath accepts "/ordarr", "ordarr/" or a full URL and returns
// "/ordarr"; empty or "/" means served at the root.
func normalizeBasePath(v string) (string, error) {
	v = strings.TrimSpace(v)
	if strings.Contains(v, "://") {
		u, err := url.Parse(v)
		if err != nil {
			return "", err
		}
		v = u.Path
	}
	v = strings.Trim(v, "/")
	if v == "" {
		return "", nil
	}
	if strings.ContainsAny(v, "?#\\\"'<>% ") || strings.Contains(v, "//") {
		return "", errors.New("ORDARR_BASE_URL must be a plain path such as /ordarr")
	}
	return "/" + v, nil
}

// Server-rendered markup only ever uses root-absolute links in these attributes,
// and html/template escapes quotes in text, so a literal match is always markup.
var (
	htmlLink = regexp.MustCompile(`((?:href|src|action|formaction)=")/([^/])`)
	cssURL   = regexp.MustCompile(`url\(/([^/])`)
)

// withBasePath serves h under base: it strips the prefix from requests and
// prefixes root-absolute redirects, HTML links and CSS urls in responses.
// shortcut: buffers HTML/CSS responses; fine for small server-rendered pages.
func withBasePath(base string, h http.Handler) http.Handler {
	if base == "" {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case p == "/healthz": // container healthcheck stays unprefixed
			h.ServeHTTP(w, r)
			return
		case p == base:
			http.Redirect(w, r, base+"/", http.StatusPermanentRedirect)
			return
		case !strings.HasPrefix(p, base+"/"):
			http.NotFound(w, r)
			return
		}
		r2 := *r
		u := *r.URL
		u.Path = strings.TrimPrefix(p, base)
		if u.RawPath != "" {
			u.RawPath = strings.TrimPrefix(u.RawPath, base)
		}
		r2.URL = &u
		rw := &rewriter{ResponseWriter: w, base: base}
		h.ServeHTTP(rw, &r2)
		rw.finish()
	})
}

type rewriter struct {
	http.ResponseWriter
	base        string
	buf         bytes.Buffer
	status      int
	wroteHeader bool
	rewrite     func([]byte) []byte
}

func (rw *rewriter) WriteHeader(code int) {
	if rw.wroteHeader {
		return
	}
	rw.wroteHeader, rw.status = true, code
	h := rw.Header()
	if loc := h.Get("Location"); strings.HasPrefix(loc, "/") && !strings.HasPrefix(loc, "//") {
		h.Set("Location", rw.base+loc)
	}
	ct := h.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "text/html"):
		rw.rewrite = func(b []byte) []byte { return htmlLink.ReplaceAll(b, []byte("${1}"+rw.base+"/${2}")) }
	case strings.HasPrefix(ct, "text/css"):
		rw.rewrite = func(b []byte) []byte { return cssURL.ReplaceAll(b, []byte("url("+rw.base+"/${1}")) }
	}
	if rw.rewrite == nil {
		rw.ResponseWriter.WriteHeader(code)
	}
}

func (rw *rewriter) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		if rw.Header().Get("Content-Type") == "" {
			rw.Header().Set("Content-Type", http.DetectContentType(b))
		}
		rw.WriteHeader(http.StatusOK)
	}
	if rw.rewrite == nil {
		return rw.ResponseWriter.Write(b)
	}
	return rw.buf.Write(b)
}

func (rw *rewriter) finish() {
	if rw.rewrite == nil {
		return
	}
	out := rw.rewrite(rw.buf.Bytes())
	rw.Header().Del("Content-Length")
	rw.ResponseWriter.WriteHeader(rw.status)
	rw.ResponseWriter.Write(out)
}
