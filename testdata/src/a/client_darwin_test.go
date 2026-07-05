package a

import "testing"

// TestClientDarwin lives in a file whose _darwin suffix is a filename-implied
// GOOS build constraint — the same exemption as an explicit //go:build line —
// so the missing client_darwin.go source is not an orphan. On darwin the file
// is in the pass and must be exempted by its name; on any other GOOS the go
// tool excludes it from the pass entirely. Either way: no diagnostic.
func TestClientDarwin(t *testing.T) { _ = t }
