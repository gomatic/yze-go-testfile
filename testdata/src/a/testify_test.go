package a

import "testing"

// Testify is a helper, not a test: the go tool treats TestXxx as a test only
// when the rune after "Test" is non-lowercase (or the name is exactly "Test"),
// so this file declares no Test functions and is exempt despite having no
// testify.go source file. A bare HasPrefix("Test") check wrongly flagged it.
func Testify(t *testing.T) { _ = t }
