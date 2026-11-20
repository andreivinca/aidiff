package clipboard

import (
	"bytes"
	"context"
	"encoding/binary"
	"unicode/utf16"
)

// windowsWriter is the Windows program that writes the clipboard, which WSL
// puts on PATH. Under WSL there is often no Wayland or X11 clipboard tool,
// and even with one, Windows' clipboard is the one a Windows terminal pastes.
const windowsWriter = "clip.exe"

// writeWindows copies text to the Windows clipboard through clip.exe, or
// reports ErrNoTool where there is none (any Linux that is not WSL).
func (s system) writeWindows(ctx context.Context, text string) error {
	if _, err := s.lookPath(windowsWriter); err != nil {
		return ErrNoTool
	}
	_, err := s.run(ctx, command{name: windowsWriter, stdin: bytes.NewReader(utf16LE(text))})
	return err
}

// utf16LE is text as UTF-16LE behind a byte-order mark: clip.exe reads
// anything else in the console's code page, which garbles non-ASCII text.
func utf16LE(text string) []byte {
	units := utf16.Encode([]rune(text))
	out := make([]byte, 0, 2+2*len(units))
	out = append(out, 0xFF, 0xFE)
	for _, u := range units {
		out = binary.LittleEndian.AppendUint16(out, u)
	}
	return out
}
