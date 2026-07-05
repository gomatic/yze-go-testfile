package a

import "testing"

// The go tool ignores files whose name begins with an underscore, so this file
// (named exactly _test.go — an empty stem) is never part of any pass and must
// never be judged. The old directory-scan model saw it in the listing and
// produced the nonsense diagnostic "has no source file .go".
func TestIgnoredUnderscoreFile(t *testing.T) { _ = t }
