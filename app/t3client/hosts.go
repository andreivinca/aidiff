package t3client

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// SSHHosts lists the hosts named in ~/.ssh/config, in file order, as
// candidates for ssh://host. Patterns (*, ?, !) are left out, and a config
// that cannot be read yields nothing.
func SSHHosts() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return sshHostsFrom(filepath.Join(home, ".ssh", "config"))
}

func sshHostsFrom(path string) []string {
	f, err := os.Open(path) //nolint:gosec // the user's own ssh config
	if err != nil {
		return nil
	}
	defer f.Close()
	var hosts []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line, _, _ := strings.Cut(sc.Text(), "#")
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.EqualFold(fields[0], "Host") {
			continue
		}
		for _, h := range fields[1:] {
			if strings.ContainsAny(h, "*?!") || seen[h] {
				continue
			}
			seen[h] = true
			hosts = append(hosts, h)
		}
	}
	return hosts
}
