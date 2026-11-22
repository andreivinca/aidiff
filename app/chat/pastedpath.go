package chat

import (
	"net/url"
	"strings"
)

// maxPastedPath bounds the text read as a pasted path; a longer paste is
// text.
const maxPastedPath = 4096

// pastedPaths reads text pasted as one line as the files it names: a
// file:// URI, or an absolute or "~/" path, quoted or backslash-escaped as
// terminals paste a dropped file, or several of those separated by spaces
// when every word is one. A path pasted bare keeps its spaces. An absolute
// path must go through a folder ("/a/b"), so a slash command ("/help") or a
// code comment ("// note") is never taken for one. nil when the text is no
// such line: relative paths and anything else stay text.
func pastedPaths(text string) []string {
	line := strings.TrimSpace(text)
	if line == "" || len(line) > maxPastedPath || strings.ContainsAny(line, "\r\n") {
		return nil
	}
	if words, ok := shellWords(line); ok && len(words) > 0 {
		paths := make([]string, 0, len(words))
		for _, w := range words {
			if p, isPath := absolutePath(w); isPath {
				paths = append(paths, p)
			}
		}
		if len(paths) == len(words) {
			return paths
		}
	}
	if p, isPath := absolutePath(line); isPath {
		return []string{p} // a path with spaces, pasted as it is
	}
	return nil
}

// attachArg reads /attach's argument as one path: quoted or escaped as a
// shell takes it, a file:// URI, or the text as it is. A relative path is
// taken, from the directory aidiff runs in.
func attachArg(arg string) string {
	arg = strings.TrimSpace(arg)
	if words, ok := shellWords(arg); ok && len(words) == 1 {
		arg = words[0]
	}
	if p, ok := fileURIPath(arg); ok {
		return p
	}
	return arg
}

// absolutePath is the path word names when it names one from the root or
// the home directory: as it is, or as a file:// URI.
func absolutePath(word string) (string, bool) {
	if p, ok := fileURIPath(word); ok {
		return p, true
	}
	rooted := strings.HasPrefix(word, "/") && !strings.HasPrefix(word, "//") && strings.Count(word, "/") >= 2
	if rooted || strings.HasPrefix(word, "~/") {
		return word, true
	}
	return "", false
}

// fileURIPath is the path of a file:// URI on this machine: its host empty
// or localhost, its path percent-decoded.
func fileURIPath(s string) (string, bool) {
	if len(s) < len("file:/") || !strings.EqualFold(s[:len("file:")], "file:") {
		return "", false
	}
	u, err := url.Parse(s)
	if err != nil || !strings.EqualFold(u.Scheme, "file") || !strings.HasPrefix(u.Path, "/") {
		return "", false
	}
	if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
		return "", false
	}
	return u.Path, true
}

// shellWords splits line into words as a shell does, for the quoting
// terminals use when they paste a dropped file: single quotes, double
// quotes, and backslashes outside single quotes. ok is false when a quote is
// left open or a backslash ends the line.
func shellWords(line string) (words []string, ok bool) {
	var word strings.Builder
	inWord := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == ' ' || c == '\t':
			if inWord {
				words, inWord = append(words, word.String()), false
				word.Reset()
			}
		case c == '\\':
			if i+1 >= len(line) {
				return nil, false
			}
			i++
			word.WriteByte(line[i])
			inWord = true
		case c == '\'':
			end := strings.IndexByte(line[i+1:], '\'')
			if end < 0 {
				return nil, false
			}
			word.WriteString(line[i+1 : i+1+end])
			i += end + 1
			inWord = true
		case c == '"':
			j := i + 1
			for ; j < len(line) && line[j] != '"'; j++ {
				if line[j] == '\\' && j+1 < len(line) && strings.IndexByte(`"\$`+"`", line[j+1]) >= 0 {
					j++
				}
				word.WriteByte(line[j])
			}
			if j >= len(line) {
				return nil, false
			}
			i = j
			inWord = true
		default:
			word.WriteByte(c)
			inWord = true
		}
	}
	if inWord {
		words = append(words, word.String())
	}
	return words, true
}
