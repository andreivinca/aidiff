package diff

import (
	"cmp"
	"path"
	"slices"
	"strings"
	"unicode"
)

// FoundPaths are the files a search by name found, best match first.
type FoundPaths struct {
	Paths     []string
	Truncated bool // more files matched than were asked for
}

// How well a path matches a query, best last.
const (
	matchNone   = iota
	matchFuzzy  // the query's characters appear in the path in order
	matchPath   // the query appears in the path as it is
	matchName   // the query appears in the file name
	matchPrefix // the file name starts with the query
)

// RankPaths keeps the paths matching query, at most limit of them (none for
// limit <= 0 means no limit), best first: a file name starting with the
// query, then one holding it, then a path holding it, then a path holding
// its characters in order. Case and spaces are ignored. Within a rank the
// shorter path comes first. An empty query keeps every path, in order.
func RankPaths(query string, paths []string, limit int) FoundPaths {
	q := strings.ToLower(strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, query))
	if q == "" {
		if limit > 0 && len(paths) > limit {
			return FoundPaths{Paths: slices.Clone(paths[:limit]), Truncated: true}
		}
		return FoundPaths{Paths: slices.Clone(paths)}
	}
	type ranked struct {
		path string
		rank int
	}
	var hits []ranked
	for _, p := range paths {
		if r := matchRank(q, p); r != matchNone {
			hits = append(hits, ranked{p, r})
		}
	}
	slices.SortStableFunc(hits, func(a, b ranked) int {
		return cmp.Or(cmp.Compare(b.rank, a.rank), cmp.Compare(len(a.path), len(b.path)), strings.Compare(a.path, b.path))
	})
	out := FoundPaths{}
	if limit > 0 && len(hits) > limit {
		hits, out.Truncated = hits[:limit], true
	}
	out.Paths = make([]string, len(hits))
	for i, h := range hits {
		out.Paths[i] = h.path
	}
	return out
}

// matchRank is how well p matches q, a lowercase query with no spaces.
func matchRank(q, p string) int {
	lp := strings.ToLower(p)
	name := path.Base(lp)
	switch {
	case strings.HasPrefix(name, q):
		return matchPrefix
	case strings.Contains(name, q):
		return matchName
	case strings.Contains(lp, q):
		return matchPath
	}
	i := 0
	for _, r := range q {
		j := strings.IndexRune(lp[i:], r)
		if j < 0 {
			return matchNone
		}
		i += j + len(string(r))
	}
	return matchFuzzy
}
