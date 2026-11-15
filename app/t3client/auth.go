package t3client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/umputun/revdiff/app/fsutil"
)

// TokenStore keeps one bearer token per T3 server origin in a 0600 JSON file.
type TokenStore struct {
	Path string
}

type tokenFile struct {
	Servers map[string]storedToken `json:"servers"`
}

type storedToken struct {
	Token   string    `json:"token"`
	SavedAt time.Time `json:"savedAt"`
}

// Get returns the saved token for baseURL, or "" when none is saved.
func (s TokenStore) Get(baseURL string) (string, error) {
	f, err := s.load()
	if err != nil {
		return "", err
	}
	return f.Servers[NormalizeURL(baseURL)].Token, nil
}

// Put saves token for baseURL, replacing any earlier one.
func (s TokenStore) Put(baseURL, token string) error {
	f, err := s.load()
	if err != nil {
		return err
	}
	f.Servers[NormalizeURL(baseURL)] = storedToken{Token: token, SavedAt: time.Now().UTC()}
	return s.save(f)
}

// Servers lists the servers with a saved token, most recently saved first.
func (s TokenStore) Servers() ([]string, error) {
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(f.Servers))
	for k := range f.Servers {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := f.Servers[keys[i]].SavedAt, f.Servers[keys[j]].SavedAt
		if a.Equal(b) {
			return keys[i] < keys[j]
		}
		return a.After(b)
	})
	return keys, nil
}

// Delete forgets the token for baseURL.
func (s TokenStore) Delete(baseURL string) error {
	f, err := s.load()
	if err != nil {
		return err
	}
	key := NormalizeURL(baseURL)
	if _, ok := f.Servers[key]; !ok {
		return nil
	}
	delete(f.Servers, key)
	return s.save(f)
}

func (s TokenStore) save(f tokenFile) error {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("encode T3 tokens: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return fmt.Errorf("create token directory: %w", err)
	}
	if err := fsutil.AtomicWriteFile(s.Path, append(b, '\n')); err != nil {
		return fmt.Errorf("save T3 token: %w", err)
	}
	return nil
}

func (s TokenStore) load() (tokenFile, error) {
	f := tokenFile{Servers: map[string]storedToken{}}
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, fmt.Errorf("read T3 tokens: %w", err)
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return f, fmt.Errorf("parse T3 tokens %s: %w", s.Path, err)
	}
	if f.Servers == nil {
		f.Servers = map[string]storedToken{}
	}
	return f, nil
}

// NormalizeURL reduces a server URL to its origin (scheme://host[:port]),
// the key tokens are saved under.
func NormalizeURL(baseURL string) string {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Host == "" {
		return strings.TrimRight(strings.TrimSpace(baseURL), "/")
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
}

// IsLoopback reports whether baseURL points at this machine.
func IsLoopback(baseURL string) bool {
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

// PairingCredential extracts the one-time credential from what `t3 pair`
// prints: either the bare credential or a URL ending in #token=<credential>.
// It also returns the origin of that URL, or "" for a bare credential.
func PairingCredential(s string) (credential, origin string) {
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

// Pair exchanges a one-time pairing credential for a bearer access token
// (OAuth token exchange at POST /oauth/token).
func Pair(ctx context.Context, baseURL, credential string) (string, error) {
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
	endpoint := strings.TrimRight(baseURL, "/") + "/oauth/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("pair with T3: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("pair with T3 at %s: %w", baseURL, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("pair with T3 at %s: HTTP %d: %s", baseURL, resp.StatusCode, strings.TrimSpace(string(body)))
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

// firstToken picks the token out of `t3 auth session issue --token-only`
// output. The t3 launcher can append runtime warnings after it.
func firstToken(out []byte) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 64*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" && !strings.ContainsAny(line, " \t[]:") {
			return line, nil
		}
	}
	return "", errors.New("t3 auth session issue printed no token")
}
