package t3client

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// SSHHosts lists the hosts named in ~/.ssh/config and the files it
// includes, in the order ssh reads them, as candidates for ssh://host.
// Patterns (*, ?, !) and names ssh://host would refuse are left out, and a
// config that cannot be read yields nothing.
func SSHHosts() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return sshHostsFrom(filepath.Join(home, ".ssh", "config"))
}

// sshHostsFrom lists the hosts of the user ssh config at path.
func sshHostsFrom(path string) []string {
	home, _ := os.UserHomeDir()
	sc := sshConfigScan{dir: filepath.Dir(path), home: home, seen: map[string]bool{}, read: map[string]bool{}}
	sc.file(path, 0)
	return sc.hosts
}

// maxIncludeDepth is how deeply ssh nests Include, as ssh itself allows.
const maxIncludeDepth = 16

// sshConfigScan collects the Host names of an ssh config and its includes.
type sshConfigScan struct {
	dir   string // where a relative Include is found: the user's ~/.ssh
	home  string // what a leading ~/ stands for
	hosts []string
	seen  map[string]bool // hosts already listed
	read  map[string]bool // files already read, so an Include loop ends
}

func (sc *sshConfigScan) file(path string, depth int) {
	if depth > maxIncludeDepth || sc.read[path] {
		return
	}
	sc.read[path] = true
	f, err := os.Open(path) //nolint:gosec // the user's own ssh config and the files it includes
	if err != nil {
		return
	}
	defer f.Close()
	lines := bufio.NewScanner(f)
	for lines.Scan() {
		keyword, args := sshConfigLine(lines.Text())
		switch strings.ToLower(keyword) {
		case "host":
			sc.add(args)
		case "include":
			for _, pattern := range args {
				sc.include(pattern, depth)
			}
		}
	}
}

func (sc *sshConfigScan) add(names []string) {
	for _, h := range names {
		if strings.ContainsAny(h, "*?!") || validSSHHost(h) != nil || sc.seen[h] {
			continue
		}
		sc.seen[h] = true
		sc.hosts = append(sc.hosts, h)
	}
}

// include reads the files an Include pattern names, in lexical order: a
// relative one is in the user's ~/.ssh, as ssh reads it.
func (sc *sshConfigScan) include(pattern string, depth int) {
	switch {
	case strings.HasPrefix(pattern, "~/") && sc.home != "":
		pattern = filepath.Join(sc.home, pattern[2:])
	case !filepath.IsAbs(pattern):
		pattern = filepath.Join(sc.dir, pattern)
	}
	matches, _ := filepath.Glob(pattern) // sorted; a malformed pattern matches nothing
	for _, m := range matches {
		sc.file(m, depth+1)
	}
}

// sshConfigLine splits an ssh config line into its keyword and arguments,
// as ssh does: the keyword ends at whitespace or an "=", and an argument may
// be in double quotes. A comment or an unterminated quote yields none.
func sshConfigLine(line string) (keyword string, args []string) {
	line, _, _ = strings.Cut(line, "#")
	line = strings.TrimSpace(line)
	end := strings.IndexAny(line, " \t=")
	if end < 0 {
		return line, nil
	}
	keyword = line[:end]
	rest := strings.TrimLeft(line[end:], " \t")
	rest = strings.TrimPrefix(rest, "=") // one "=", with whitespace around it or not
	for {
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" {
			return keyword, args
		}
		var arg string
		if quoted, ok := strings.CutPrefix(rest, `"`); ok {
			var closed bool
			if arg, rest, closed = strings.Cut(quoted, `"`); !closed {
				return keyword, nil
			}
		} else if i := strings.IndexAny(rest, " \t"); i >= 0 {
			arg, rest = rest[:i], rest[i:]
		} else {
			arg, rest = rest, ""
		}
		args = append(args, arg)
	}
}
