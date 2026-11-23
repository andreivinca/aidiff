package t3client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Probe reports whether a T3 server answers at spec, the way Connect would
// reach it, without logging in: for local, the server this user runs here;
// for ssh://host, a T3 Code running on host, which costs an SSH login; for
// a URL, any HTTP answer there. nil means it answers.
func Probe(ctx context.Context, spec string) error {
	srv, err := parseServer(spec)
	if err != nil {
		return err
	}
	switch srv.kind {
	case serverLocal:
		origin, err := LocalOrigin()
		if err != nil {
			return err
		}
		return probeHTTP(ctx, origin)
	case serverSSH:
		_, err := remoteState(ctx, srv.sshHost)
		return err
	default: // serverHTTP
		return probeHTTP(ctx, srv.base)
	}
}

// probeHTTP asks base for its session state: a server answers without a
// token too (401), and any answer short of a server error is one.
func probeHTTP(ctx context.Context, base string) error {
	ep := directEndpoint(base)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/api/auth/session", http.NoBody)
	if err != nil {
		return fmt.Errorf("probe %s: %w", base, err)
	}
	resp, err := ep.client.Do(req)
	if err != nil {
		return fmt.Errorf("probe %s: %w", base, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= http.StatusInternalServerError {
		return fmt.Errorf("probe %s: HTTP %d", base, resp.StatusCode)
	}
	return nil
}

// PairingLink reports whether s is a link `t3 pair` prints, an http(s) URL
// carrying a token (https://host:port/pair#token=…), and the server it
// names: its origin, as Connect pairs with it. A bare credential names no
// server and is no link.
func PairingLink(s string) (spec string, ok bool) {
	cred, _ := pairingCredential(s)
	u, err := url.Parse(strings.TrimSpace(s))
	if cred == "" || err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	return serverBase(&url.URL{Scheme: u.Scheme, Host: u.Host}), true
}
