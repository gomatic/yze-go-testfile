package c_test // want `zzz_test.go`

import "testing"

// TestZzz is a genuine unit-test orphan: no zzz.go source, no build tag, and it
// declares a Test function. The package has no non-test .go file (GoFiles empty),
// so it is delivered only through the external-test pass; the orphan is reported
// there, anchored at this file's own package clause.
func TestZzz(t *testing.T) { _ = t }
