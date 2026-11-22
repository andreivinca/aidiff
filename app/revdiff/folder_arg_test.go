package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gitIn runs git in dir with a fixed identity and no user config.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // test-controlled arguments
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

// folderRepo is a git repository with one commit, a branch named main, a
// folder docs, a folder named like the branch (main) and a file README.
func folderRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	for _, d := range []string{"docs", "main"} {
		require.NoError(t, os.MkdirAll(filepath.Join(repo, d), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(repo, d, "x.txt"), []byte(d+"\n"), 0o600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README"), []byte("hi\n"), 0o600))
	gitIn(t, repo, "init", "-q", "-b", "main")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-qm", "init")
	return repo
}

func TestFolderForm(t *testing.T) {
	for _, arg := range []string{".", "..", "./x", "../x", "/abs/path", "~", "~/src", "dir/", "a/b/"} {
		assert.True(t, folderForm(arg), "%q is written as a path", arg)
	}
	for _, arg := range []string{"", "main", "HEAD~3", "HEAD^", "a..b", "a...b", "feature/x", "v1.2", "@{u}", "abc123"} {
		assert.False(t, folderForm(arg), "%q is no path: a ref", arg)
	}
}

func TestParseArgs_FolderArgument(t *testing.T) {
	parse := func(args ...string) (options, error) {
		return parseArgs(append(noConfigArgs(t), args...))
	}
	opts, err := parse(".")
	require.NoError(t, err)
	assert.Equal(t, ".", opts.folder, "a folder, not a ref")
	assert.Empty(t, opts.ref())

	opts, err = parse("--staged", "./app")
	require.NoError(t, err)
	assert.Equal(t, "./app", opts.folder, "--staged reviews that folder's staged changes")
	assert.True(t, opts.Staged)

	opts, err = parse("--all-files", "..")
	require.NoError(t, err, "a folder is no ref: --all-files browses it")
	assert.Equal(t, "..", opts.folder)

	opts, err = parse("--terminal", ".")
	require.NoError(t, err)
	assert.Equal(t, ".", opts.folder)
	assert.True(t, opts.Terminal)
	opts, err = parse(".", "--terminal")
	require.NoError(t, err)
	assert.Equal(t, ".", opts.folder, "in either order")

	for _, refs := range [][]string{{"main"}, {"HEAD~3"}, {"a", "b"}, {"a..b"}, {"main", "./x"}} {
		opts, err = parse(refs...)
		require.NoError(t, err)
		assert.Empty(t, opts.folder, "%v stays refs", refs)
		assert.Equal(t, refs[0], opts.Refs.Base)
	}
	opts, err = parse("--staged", "main")
	require.NoError(t, err)
	assert.Equal(t, "main", opts.ref())

	for flag, args := range map[string][]string{
		"--stdin":                     {"--stdin", "."},
		"--only":                      {"--only", "a.go", "."},
		"--compare-old/--compare-new": {"--compare-old", "go.mod", "--compare-new", "go.sum", "./x"},
	} {
		_, err = parse(args...)
		require.Error(t, err, flag)
		assert.Contains(t, err.Error(), "cannot be used with "+flag)
	}
}

func TestResolveStartArg_FolderOrRef(t *testing.T) {
	repo := folderRepo(t)
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "src"), 0o750))
	t.Setenv("HOME", home)

	resolve := func(args ...string) (options, error) {
		opts, err := parseArgs(append(noConfigArgs(t), args...))
		require.NoError(t, err)
		return resolveStartArg(opts, repo)
	}
	for arg, want := range map[string]string{
		".":                         repo,
		"..":                        filepath.Dir(repo),
		"./docs":                    filepath.Join(repo, "docs"),
		"docs/":                     filepath.Join(repo, "docs"),
		filepath.Join(repo, "docs"): filepath.Join(repo, "docs"),
		"~/src":                     filepath.Join(home, "src"),
		"~":                         home,
		"docs":                      filepath.Join(repo, "docs"), // a folder and no revision
		"./main":                    filepath.Join(repo, "main"), // the folder, written as one
		"main/":                     filepath.Join(repo, "main"),
	} {
		opts, err := resolve(arg)
		require.NoError(t, err, arg)
		assert.Equal(t, want, opts.folder, "%q", arg)
		assert.Empty(t, opts.ref(), "%q is no ref", arg)
	}

	for _, refs := range [][]string{{"main"}, {"HEAD"}, {"HEAD~0"}, {"main..HEAD"}, {"main", "HEAD"}} {
		opts, err := resolve(refs...)
		require.NoError(t, err, "%v", refs)
		assert.Empty(t, opts.folder, "%v stays refs: a branch named like a folder is the branch", refs)
	}
	opts, err := resolve("--staged", "HEAD")
	require.NoError(t, err)
	assert.Equal(t, "HEAD", opts.ref())

	_, err = resolve("nope")
	require.EqualError(t, err, `"nope" is neither a folder nor a git revision`)
	_, err = resolve("./nope")
	require.EqualError(t, err, "folder ./nope does not exist")
	_, err = resolve("./README")
	require.EqualError(t, err, "./README is not a folder")

	plain := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(plain, "notes"), 0o750))
	opts, err = parseArgs(append(noConfigArgs(t), "notes"))
	require.NoError(t, err)
	opts, err = resolveStartArg(opts, plain)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(plain, "notes"), opts.folder, "outside a repository a folder's name is the folder")
	opts, err = parseArgs(append(noConfigArgs(t), "main"))
	require.NoError(t, err)
	_, err = resolveStartArg(opts, plain)
	require.EqualError(t, err, `"main" is not a folder, and a ref needs a repository`)
}

func TestResolveStartArg_GitThatCannotAnswerKeepsTheRef(t *testing.T) {
	repo := folderRepo(t)
	old := isRevision
	isRevision = func(string, string) revAnswer { return revUnknown }
	t.Cleanup(func() { isRevision = old })
	opts, err := parseArgs(append(noConfigArgs(t), "docs"))
	require.NoError(t, err)
	opts, err = resolveStartArg(opts, repo)
	require.NoError(t, err)
	assert.Equal(t, "docs", opts.ref(), "a ref, as before, when git cannot say")
}
