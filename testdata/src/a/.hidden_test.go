package a

import "testing"

// The go tool ignores files whose name begins with a dot, so this file is never
// part of any pass and must never be judged, despite having no .hidden.go source.
func TestIgnoredDotFile(t *testing.T) { _ = t }
