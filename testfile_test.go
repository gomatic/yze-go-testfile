package testfile

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
)

// fakeFiles returns a fileReader serving the given contents, erroring on unknown
// paths.
func fakeFiles(contents map[string]string) fileReader {
	return func(path string) ([]byte, error) {
		if content, ok := contents[path]; ok {
			return []byte(content), nil
		}
		return nil, errors.New("no such file")
	}
}

// fakeDir returns a dirReader listing the base names of the given paths.
func fakeDir(contents map[string]string) dirReader {
	return func(dirPath) ([]string, error) {
		names := make([]string, 0, len(contents))
		for path := range contents {
			names = append(names, filepath.Base(path))
		}
		return names, nil
	}
}

// parseAll parses the named paths from contents into fset, in order, standing in
// for the syntax files a pass delivers.
func parseAll(t *testing.T, fset *token.FileSet, contents map[string]string, paths ...string) []*ast.File {
	t.Helper()
	files := make([]*ast.File, 0, len(paths))
	for _, path := range paths {
		parsed, err := parser.ParseFile(fset, path, contents[path], parser.ParseComments|parser.SkipObjectResolution)
		require.NoError(t, err)
		files = append(files, parsed)
	}
	return files
}

// passContents is a fake package exercising every classification the judging
// distinguishes, keyed by path.
func passContents() map[string]string {
	return map[string]string{
		// A source file in the pass is never judged.
		"d/a.go": "package a\n\nfunc A() int { return 1 }",
		// A unit test with a matching source file is fine.
		"d/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) { _ = A() }",
		// A unit test with no source file: a genuine orphan.
		"d/helper_test.go": "package a\nimport \"testing\"\nfunc TestHelper(t *testing.T) {}",
		// Examples/benchmarks declare no Test function and are exempt.
		"d/example_test.go": "package a\nfunc ExampleA() {}",
		// A //go:build constraint marks an integration test, which is exempt.
		"d/integration_test.go": "//go:build integration\n\npackage a\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}",
		// The legacy // +build form is equally a build constraint and exempt
		// (the substring check missed it, wrongly flagging legacy orphans).
		"d/legacy_test.go": "// +build integration\n\npackage a\n\nimport \"testing\"\n\nfunc TestLegacy(t *testing.T) {}",
		// A real unit-test orphan whose comment and string literals merely
		// mention //go:build and func Test as text: neither is structural, so
		// the file is still an orphan (the substring check wrongly exempted it).
		"d/comment_test.go": "package a\n\nimport \"testing\"\n\n// mentions //go:build integration and func TestFake as text only\nfunc TestComment(t *testing.T) {\n\t_ = \"//go:build integration\"\n\t_ = \"func TestFake(t *testing.T)\"\n\t_ = t\n}",
		// A _GOOS filename suffix is a filename-implied build constraint, so the
		// missing client_darwin.go source is exempt (the name-blind check
		// wrongly flagged it).
		"d/client_darwin_test.go": "package a\nimport \"testing\"\nfunc TestClient(t *testing.T) {}",
		// Testify is a helper, not a test (lowercase rune after "Test"), so the
		// file declares no Test functions and is exempt (the bare HasPrefix
		// check wrongly counted it).
		"d/testify_test.go": "package a\nimport \"testing\"\nfunc Testify(t *testing.T) {}",
	}
}

func TestOrphansClassifiesEveryPassFile(t *testing.T) {
	contents := passContents()
	paths := []string{
		"d/a.go",
		"d/a_test.go",
		"d/helper_test.go",
		"d/example_test.go",
		"d/integration_test.go",
		"d/legacy_test.go",
		"d/comment_test.go",
		"d/client_darwin_test.go",
		"d/testify_test.go",
	}
	fset := token.NewFileSet()
	files := parseAll(t, fset, contents, paths...)

	found := orphans(fakeDir(contents), fakeFiles(contents), fset, files)

	stems := make([]string, 0, len(found))
	for _, o := range found {
		stems = append(stems, string(o.stem))
	}
	assert.ElementsMatch(t, []string{"helper", "comment"}, stems)
}

func TestOrphansAnchorsEachReportAtItsOwnPackageClause(t *testing.T) {
	contents := passContents()
	fset := token.NewFileSet()
	files := parseAll(t, fset, contents, "d/helper_test.go", "d/comment_test.go")

	found := orphans(fakeDir(contents), fakeFiles(contents), fset, files)

	require.Len(t, found, 2)
	assert.Equal(t, files[0].Name.Pos(), found[0].pos)
	assert.Equal(t, "helper", string(found[0].stem))
	assert.Equal(t, files[1].Name.Pos(), found[1].pos)
	assert.Equal(t, "comment", string(found[1].stem))
}

// TestOrphansJudgesOnlyFilesInThePass pins the reporting model: a file is judged
// only by the pass that contains it. The directory listing shows zzz_test.go,
// but the pass under judgment does not deliver it, so it is not reported here —
// its own pass reports it, which is what eliminates cross-pass duplicates.
func TestOrphansJudgesOnlyFilesInThePass(t *testing.T) {
	contents := map[string]string{
		"d/helper_test.go": "package a\nimport \"testing\"\nfunc TestHelper(t *testing.T) {}",
		"d/zzz_test.go":    "package a\nimport \"testing\"\nfunc TestZzz(t *testing.T) {}",
	}
	fset := token.NewFileSet()
	files := parseAll(t, fset, contents, "d/helper_test.go")

	found := orphans(fakeDir(contents), fakeFiles(contents), fset, files)

	require.Len(t, found, 1)
	assert.Equal(t, "helper", string(found[0].stem))
}

func TestJudgeableRecognizesIgnoredAndConstrainedNames(t *testing.T) {
	cases := map[fileName]bool{
		// Ordinary unit-test files are judgeable.
		"helper_test.go":  true,
		"foo_bar_test.go": true,
		// A GOOS/GOARCH word needs a non-empty prefix to constrain, so a bare
		// linux_test.go is an ordinary judgeable test file.
		"linux_test.go": true,
		// A trailing non-GOOS/GOARCH part does not constrain.
		"client_linux_foo_test.go": true,
		// Non-test files are never judged.
		"a.go":      false,
		"notgo.txt": false,
		// Files the go tool ignores: empty stem, leading _ or leading dot.
		"_test.go":     false,
		"_foo_test.go": false,
		".foo_test.go": false,
		// Filename-implied build constraints: _GOOS, _GOARCH, _GOOS_GOARCH, and
		// a GOARCH match after an ignored prefix.
		"client_darwin_test.go":      false,
		"client_amd64_test.go":       false,
		"client_linux_arm64_test.go": false,
		"linux_amd64_test.go":        false,
	}
	for name, want := range cases {
		_, ok := judgeable(name)
		assert.Equal(t, want, ok, string(name))
	}
}

func TestJudgeableReturnsTheStem(t *testing.T) {
	stem, ok := judgeable("helper_test.go")
	require.True(t, ok)
	assert.Equal(t, "helper", string(stem))
}

// TestOrphanTreatsUnreadableDirectoryAsNotMissing pins the fail-open contract on
// the directory side: when the listing cannot be read, the source counterpart is
// not treated as missing and no report is produced.
func TestOrphanTreatsUnreadableDirectoryAsNotMissing(t *testing.T) {
	dir := func(dirPath) ([]string, error) { return nil, errors.New("unreadable") }
	file := fakeFiles(map[string]string{
		"d/helper_test.go": "package a\nimport \"testing\"\nfunc TestHelper(t *testing.T) {}",
	})

	_, ok := orphan(dir, file, "d/helper_test.go")

	assert.False(t, ok)
}

// The unreadable/unparseable exemptions pin the deliberate fail-open contract:
// the analyzer never reports a file it could not inspect.
func TestExemptTreatsUnreadableFileAsExempt(t *testing.T) {
	assert.True(t, exempt(fakeFiles(nil), "missing.go"))
}

func TestExemptTreatsUnparseableFileAsExempt(t *testing.T) {
	file := fakeFiles(map[string]string{"d/broken_test.go": "package a\nfunc TestBroken(t *testing.T {"})

	assert.True(t, exempt(file, "d/broken_test.go"))
}

func TestExemptIgnoresMethodsNamedLikeTests(t *testing.T) {
	file := fakeFiles(map[string]string{"d/method_test.go": "package a\ntype S struct{}\nfunc (S) TestThing() {}"})

	assert.True(t, exempt(file, "d/method_test.go"))
}

// TestExemptAppliesTheGoTestNameRule pins the go tool's rule for what counts as
// a Test function: "Test" followed by a non-lowercase rune, or exactly "Test".
func TestExemptAppliesTheGoTestNameRule(t *testing.T) {
	cases := map[string]struct {
		content string
		exempt  bool
	}{
		"exactly Test is a test":          {"package a\nimport \"testing\"\nfunc Test(t *testing.T) {}", false},
		"TestX is a test":                 {"package a\nimport \"testing\"\nfunc TestX(t *testing.T) {}", false},
		"Test_helper is a test":           {"package a\nimport \"testing\"\nfunc Test_helper(t *testing.T) {}", false},
		"Testify is a helper, not a test": {"package a\nimport \"testing\"\nfunc Testify(t *testing.T) {}", true},
		"Benchmark-only file is exempt":   {"package a\nimport \"testing\"\nfunc BenchmarkA(b *testing.B) {}", true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			file := fakeFiles(map[string]string{"d/x_test.go": tc.content})

			assert.Equal(t, tc.exempt, exempt(file, "d/x_test.go"))
		})
	}
}

func TestOSReadDirNames(t *testing.T) {
	names, err := osReadDirNames("testdata/src/a")
	require.NoError(t, err)
	assert.Contains(t, names, "a.go")

	_, err = osReadDirNames("testdata/does-not-exist")
	require.Error(t, err)
}

func TestRunReportsOrphanUnitTestFiles(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), Analyzer, "a")
}

// TestRunSkipsPackageWithoutFiles pins the contract that a pass carrying no
// syntax files is a no-op. The driver delivers such a pass for a package whose
// only Go files are external test files (an examples-only directory); under the
// per-file judging model there is nothing to judge and nothing to report.
func TestRunSkipsPackageWithoutFiles(t *testing.T) {
	result, err := run(&analysis.Pass{})

	require.NoError(t, err)
	assert.Nil(t, result)
}

// TestRunSkipsExternalTestOnlyExamplesPackage reproduces the cmd-cat/examples
// layout end-to-end: a directory of only external test files declaring only
// Example functions. The base package has zero syntax files; the analyzer must
// run clean and report nothing, since every example is exempt.
func TestRunSkipsExternalTestOnlyExamplesPackage(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), Analyzer, "b")
}

// TestRunReportsOrphanInExternalTestOnlyPackage proves the empty-pass no-op does
// not blind detection: a genuine unit-test orphan living in a package with no
// non-test source (delivered only via the external-test pass) is still flagged,
// anchored at its own package clause.
func TestRunReportsOrphanInExternalTestOnlyPackage(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), Analyzer, "c")
}

func TestRegistrationIsWellFormed(t *testing.T) {
	assert.NoError(t, Registration.Validate())
	assert.Equal(t, "yze/testfile", Registration.RuleID())
	assert.Same(t, Analyzer, Registration.Analyzer)
}
