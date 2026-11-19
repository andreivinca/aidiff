package diff

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRankPaths(t *testing.T) {
	paths := []string{"docs/model.md", "app/ui/model.go", "app/ui/remodel.go", "app/modeling/x.go", "app/ui/mdl.go", "README.md"}
	found := RankPaths("model", paths, 0)
	assert.Equal(t, []string{"docs/model.md", "app/ui/model.go", "app/ui/remodel.go", "app/modeling/x.go"}, found.Paths,
		"a name starting with the query, then one holding it, then a path holding it; shorter first within each")
	assert.False(t, found.Truncated)

	assert.Equal(t, []string{"app/ui/mdl.go", "app/ui/model.go", "app/ui/remodel.go"}, RankPaths("ui MDL", paths, 0).Paths,
		"case and spaces are ignored, characters in order match too")
	assert.Empty(t, RankPaths("zzz", paths, 0).Paths)

	found = RankPaths("model", paths, 2)
	assert.Equal(t, []string{"docs/model.md", "app/ui/model.go"}, found.Paths)
	assert.True(t, found.Truncated, "more matched than the limit")

	found = RankPaths(" ", paths, 3)
	assert.Equal(t, paths[:3], found.Paths, "an empty query keeps the order")
	assert.True(t, found.Truncated)
}
