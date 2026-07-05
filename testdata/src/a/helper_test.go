package a // want `helper_test.go`

import "testing"

// TestHelper is a unit test with no helper.go source file: an orphan, anchored
// at this file's own package clause.
func TestHelper(t *testing.T) { _ = t }
