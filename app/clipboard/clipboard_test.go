package clipboard

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPickImage(t *testing.T) {
	assert.Equal(t, "image/png", pickImage("text/plain\nimage/jpeg\nimage/png\n"), "png wins")
	assert.Equal(t, "image/jpeg", pickImage("TEXT image/jpeg image/webp"), "else the first image type")
	assert.Empty(t, pickImage("text/plain\nUTF8_STRING\n"))
	assert.Empty(t, pickImage(""))
}
