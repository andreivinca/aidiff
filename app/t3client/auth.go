package t3client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
)

// maxErrorBody bounds how much of a failed response an error quotes.
const maxErrorBody = 200

// normalizeURL reduces a server URL to its origin (scheme://host[:port]),
// the key tokens are saved under.
func normalizeURL(baseURL string) string {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Host == "" {
		return strings.TrimRight(strings.TrimSpace(baseURL), "/")
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
}

// isLoopback reports whether baseURL points at this machine.
func isLoopback(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// pairingCredential extracts the one-time credential from what `t3 pair`
// prints: either the bare credential or a URL ending in #token=<credential>.
// It also returns the origin of that URL, or "" for a bare credential.
func pairingCredential(s string) (credential, origin string) {
	s = strings.TrimSpace(s)
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return s, ""
	}
	frag, _ := url.ParseQuery(u.Fragment)
	cred := frag.Get("token")
	if cred == "" {
		cred = u.Query().Get("token")
	}
	return cred, u.Scheme + "://" + u.Host
}

// pair exchanges a one-time pairing credential for a bearer access token
// (OAuth token exchange at POST /oauth/token). The endpoint's client never
// follows a redirect, which would carry the credential elsewhere.
func pair(ctx context.Context, ep endpoint, credential string) (string, error) {
	if credential == "" {
		return "", errors.New("empty T3 pairing credential")
	}
	form := url.Values{
		"grant_type":           {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":        {credential},
		"subject_token_type":   {"urn:t3:params:oauth:token-type:environment-bootstrap"},
		"requested_token_type": {"urn:ietf:params:oauth:token-type:access_token"},
		"client_label":         {"aidiff"},
		"client_device_type":   {"bot"},
	}
	endpointURL := strings.TrimRight(ep.origin, "/") + "/oauth/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("pair with T3: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := ep.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("pair with T3 at %s: %w", ep.origin, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("pair with T3 at %s: read response: %w", ep.origin, err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("pair with T3 at %s: HTTP %d: %s", ep.origin, resp.StatusCode, errorExcerpt(body))
	}
	var out struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("pair with T3: decode response: %w", err)
	}
	if out.AccessToken == "" {
		return "", errors.New("pair with T3: no access token in response")
	}
	if !strings.EqualFold(out.TokenType, "Bearer") {
		return "", fmt.Errorf("pair with T3: server issued a %s token; aidiff needs a Bearer token", out.TokenType)
	}
	return out.AccessToken, nil
}

// errorExcerpt is the start of a failed response's body, enough to explain it.
func errorExcerpt(body []byte) string {
	text := []rune(strings.TrimSpace(string(body)))
	if len(text) <= maxErrorBody {
		return string(text)
	}
	return string(text[:maxErrorBody]) + "…"
}

// issueArgs asks `t3 auth session issue` for a token with just the scopes a
// review needs.
var issueArgs = []string{"auth", "session", "issue",
	"--label", "aidiff", "--ttl", "30d", "--token-only",
	"--scope", "orchestration:read", "--scope", "orchestration:operate", "--scope", "filesystem:read"}

// issueLocal asks the t3 CLI on this machine for a bearer token. It works for
// a server whose data directory this user owns, which is the loopback case.
func issueLocal(ctx context.Context) (string, error) {
	t3, err := exec.LookPath("t3")
	if err != nil {
		return "", errors.New("no T3 token saved and the t3 CLI is not on PATH; pair with --t3-pair")
	}
	return runIssue(exec.CommandContext(ctx, t3, issueArgs...)) //nolint:gosec // fixed arguments, binary from PATH
}

// runIssue runs a token-issuing command and extracts the token it prints.
func runIssue(cmd *exec.Cmd) (string, error) {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("t3 auth session issue: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return firstToken(out)
}

// tokenFormat is what an issued token looks like: base64url segments joined
// by dots.
var tokenFormat = regexp.MustCompile(`^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)*$`)

// firstToken reads the token out of `t3 auth session issue --token-only`
// output: its first non-empty line, which the t3 launcher may follow with
// runtime warnings. A first line that is not a token is an error, not a
// reason to look further.
func firstToken(out []byte) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 64*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if !tokenFormat.MatchString(line) {
			return "", fmt.Errorf("t3 auth session issue printed %q where a token belongs", errorExcerpt([]byte(line)))
		}
		return line, nil
	}
	return "", errors.New("t3 auth session issue printed no token")
}
