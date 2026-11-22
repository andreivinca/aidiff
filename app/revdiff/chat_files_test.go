package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/chat"
)

func TestChatFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	require.NoError(t, os.WriteFile(path, []byte("hello"), 0o600))
	var files chat.Files = chatFiles{}

	info, err := files.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, chat.FileInfo{Name: "notes.txt", Size: 5, MIME: "text/plain"}, info)

	rc, size, err := files.Open(path)
	require.NoError(t, err)
	defer rc.Close()
	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, int64(5), size)
	assert.Equal(t, "hello", string(data))

	_, err = files.Stat(dir)
	require.ErrorContains(t, err, "is a folder")
	_, err = files.ReadImage(path, func(string, int64) error { return nil })
	require.Error(t, err, "a text file is no image")
}
