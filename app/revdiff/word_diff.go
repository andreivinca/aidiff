package main

// aidiff highlights intra-line word changes by default; --no-word-diff
// turns it off. revdiff's --word-diff stays accepted, and is then a no-op.

// wordDiff reports whether reviews start with word-level highlighting.
func (o options) wordDiff() bool {
	return o.WordDiff || !o.NoWordDiff
}
