package clipboard

import (
	"net/url"
	"strings"
)

// fileListTypes are the types a file manager offers for files it copied,
// best first: the standard URI list (RFC 2483), then the desktops' own,
// which hold the same URIs after a "copy" or "cut" line.
var fileListTypes = []string{
	"text/uri-list",
	"x-special/gnome-copied-files",
	"x-special/kde-copied-files",
	"application/x-kde4-urilist",
}

// pickFileList chooses the file list type to read from the offered ones,
// spelled as offered, since X11 targets are names to match exactly; "" when
// none is offered.
func pickFileList(types string) string {
	offered := strings.Fields(types)
	for _, want := range fileListTypes {
		for _, t := range offered {
			if strings.EqualFold(t, want) {
				return t
			}
		}
	}
	return ""
}

// fileList is the paths of the local files a URI list names, in order: one
// URI a line (CRLF or LF), "#" lines are comments, and lines that are no
// file:// URI on this machine are left out (a web address, a desktop's
// "copy" line, a file on another host). host is this machine's name, which
// a file URI may carry instead of none or "localhost". The paths are
// percent-decoded.
func fileList(data, host string) []string {
	var out []string
	for line := range strings.SplitSeq(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if path, ok := localFile(line, host); ok {
			out = append(out, path)
		}
	}
	return out
}

// localFile is the path of a file:// URI that names a file on this
// machine.
func localFile(uri, host string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil || !strings.EqualFold(u.Scheme, "file") || !strings.HasPrefix(u.Path, "/") {
		return "", false
	}
	if u.Host != "" && !strings.EqualFold(u.Host, "localhost") && (host == "" || !strings.EqualFold(u.Host, host)) {
		return "", false
	}
	return u.Path, true
}
