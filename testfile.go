// Package testfile provides a go/analysis analyzer enforcing the gomatic Go
// testing standard that unit-test files are 1:1 with their source files:
// <name>_test.go tests <name>.go. A _test.go file without a matching source file
// is allowed only when it is not a unit test — it carries a build constraint
// (an explicit //go:build or legacy // +build line, or a filename-implied
// _GOOS/_GOARCH suffix) or declares no Test functions (examples, benchmarks,
// fuzz targets).
//
// Each pass judges only the _test.go files it delivers, so a file is reported
// exactly once — by the pass that contains it — anchored at its own package
// clause. Files the go tool ignores (a leading _ or dot, including a file named
// exactly _test.go) are never judged. Unreadable or unparseable test files are
// deliberately exempt: the analyzer fails open rather than report a file it
// could not inspect, and that contract is pinned by tests.
package testfile

import (
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	goyze "github.com/gomatic/go-yze"
	"golang.org/x/tools/go/analysis"
)

// Injected filesystem operations, so the analyzer's error and decision paths are
// testable without a real directory tree.
type (
	dirReader  func(dir dirPath) ([]string, error)
	fileReader func(path string) ([]byte, error)
)

var (
	readDir  dirReader  = osReadDirNames
	readFile fileReader = os.ReadFile
)

// Analyzer reports unit-test files that are not 1:1 with a source file.
var Analyzer = &analysis.Analyzer{
	Name: "testfile",
	Doc:  "reports _test.go unit-test files without a matching source file",
	Run:  run,
}

// Registration declares this analyzer to the yze framework.
var Registration = goyze.Registration{
	Name:       "testfile",
	Categories: []goyze.Category{"testing"},
	URL:        "https://docs.gomatic.dev/yze/testfile",
	Analyzer:   Analyzer,
}

// run judges each _test.go file delivered in the pass and reports the orphans.
// A file is only ever judged by the pass that contains it (base package,
// in-package test variant, or external test package), so each orphan is reported
// exactly once, anchored at its own package clause. A pass with no syntax files
// (a package whose only Go files are external test files, e.g. an examples-only
// directory) is a no-op by construction.
func run(pass *analysis.Pass) (any, error) {
	for _, found := range orphans(readDir, readFile, pass.Fset, pass.Files) {
		pass.Reportf(
			found.pos,
			"test file %s_test.go has no source file %s.go; unit tests must be 1:1 with their source (give integration tests a build tag, or keep only examples/benchmarks/fuzz)",
			found.stem,
			found.stem,
		)
	}
	return nil, nil
}

// report is an orphan diagnostic to issue: the stem of the orphan test file,
// anchored at that file's own package clause.
type report struct {
	stem stemName
	pos  token.Pos
}

// orphans judges each syntax file delivered in a pass and returns a report for
// every orphan unit-test file among them, in pass order. Files not in the pass
// are never judged, so no other pass over the same directory can duplicate a
// report.
func orphans(dir dirReader, file fileReader, fset *token.FileSet, files []*ast.File) []report {
	var found []report
	for _, f := range files {
		if stem, ok := orphan(dir, file, filePath(fset.File(f.Pos()).Name())); ok {
			found = append(found, report{stem: stem, pos: f.Name.Pos()})
		}
	}
	return found
}

// dirPath is the filesystem path of the analyzed package's directory.
type dirPath string

// filePath is the filesystem path of a test file under judgment.
type filePath string

// orphan reports a test file's stem when it is a unit test with no source file.
func orphan(dir dirReader, file fileReader, path filePath) (stemName, bool) {
	stem, ok := judgeable(fileName(filepath.Base(string(path))))
	if !ok || !missingSource(dir, path, stem) || exempt(file, path) {
		return "", false
	}
	return stem, true
}

// fileName is a bare file name within the package directory.
type fileName string

// stemName is a test file's base name with the _test.go suffix stripped: the
// name of the source file it must be 1:1 with.
type stemName string

// judgeable returns the stem of a file name that is subject to the rule: a
// _test.go file the go tool does not ignore, without a filename-implied
// GOOS/GOARCH build constraint.
func judgeable(name fileName) (stemName, bool) {
	stem, ok := testStem(name)
	if !ok || ignored(stem) || constrained(stem) {
		return "", false
	}
	return stem, true
}

// testStem returns the stem of a _test.go file name.
func testStem(name fileName) (stemName, bool) {
	if !strings.HasSuffix(string(name), "_test.go") {
		return "", false
	}
	return stemName(strings.TrimSuffix(string(name), "_test.go")), true
}

// ignored reports whether the go tool ignores the file: an empty stem (a file
// named exactly _test.go) or a name beginning with _ or a dot.
func ignored(stem stemName) bool {
	return stem == "" || stem[0] == '_' || stem[0] == '.'
}

// constrained reports whether the stem (the file name with _test.go already
// stripped) carries a filename-implied GOOS/GOARCH build constraint, per the go
// tool's rule (go/build's Context.goodOSArchFile): everything before the first
// underscore is ignored, then the trailing _GOOS_GOARCH, _GOOS, or _GOARCH
// parts constrain — so client_darwin_test.go is constrained but linux_test.go
// is not (the tag needs a non-empty prefix).
func constrained(stem stemName) bool {
	i := strings.Index(string(stem), "_")
	if i < 0 {
		return false
	}
	parts := strings.Split(string(stem[i+1:]), "_")
	n := len(parts)
	if n >= 2 && knownOS[osArch(parts[n-2])] && knownArch[osArch(parts[n-1])] {
		return true
	}
	return knownOS[osArch(parts[n-1])] || knownArch[osArch(parts[n-1])]
}

// missingSource reports whether the stem's source counterpart is absent from the
// test file's directory. An unreadable directory is never treated as missing, so
// filesystem failures produce no report (fail-open, matching exempt).
func missingSource(dir dirReader, path filePath, stem stemName) bool {
	names, err := dir(dirPath(filepath.Dir(string(path))))
	return err == nil && !slices.Contains(names, string(stem)+".go")
}

// exempt reports whether a test file is not a unit test: it carries a build
// constraint (a //go:build or legacy // +build line), or declares no Test
// functions. The file is parsed so that build-constraint lines and Test
// function declarations are recognized structurally — never by substring, which
// both misses the legacy // +build form and misfires on text appearing inside a
// comment or string literal. An unreadable or unparseable file is deliberately
// exempt (fail-open): the analyzer never reports a file it could not inspect.
func exempt(file fileReader, path filePath) bool {
	content, err := file(string(path))
	if err != nil {
		return true
	}
	parsed, err := parser.ParseFile(
		token.NewFileSet(),
		string(path),
		content,
		parser.ParseComments|parser.SkipObjectResolution,
	)
	if err != nil {
		return true
	}
	return hasBuildConstraint(parsed) || !hasTestFunc(parsed)
}

// hasBuildConstraint reports whether the file carries a build constraint, in
// either the //go:build or the legacy // +build form, before its package clause.
func hasBuildConstraint(f *ast.File) bool {
	for _, group := range f.Comments {
		if constrains(group, f.Package) {
			return true
		}
	}
	return false
}

// constrains reports whether a comment group holds a build-constraint line that
// precedes the package clause at pkg.
func constrains(group *ast.CommentGroup, pkg token.Pos) bool {
	for _, c := range group.List {
		if c.Pos() < pkg && (constraint.IsGoBuild(c.Text) || constraint.IsPlusBuild(c.Text)) {
			return true
		}
	}
	return false
}

// hasTestFunc reports whether the file declares at least one Test function.
func hasTestFunc(f *ast.File) bool {
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && isTestFunc(fn) {
			return true
		}
	}
	return false
}

// isTestFunc reports whether a declaration is a top-level Test function (a
// TestXxx free function, not a method).
func isTestFunc(fn *ast.FuncDecl) bool {
	return fn.Recv == nil && isTestName(funcName(fn.Name.Name))
}

// funcName is the identifier of a declared function.
type funcName string

// isTestName applies the go tool's rule for test identifiers: "Test" followed by
// a non-lowercase rune, or exactly "Test" — so Testify is a helper, not a test.
func isTestName(name funcName) bool {
	rest, ok := strings.CutPrefix(string(name), "Test")
	if !ok {
		return false
	}
	r, _ := utf8.DecodeRuneInString(rest)
	return rest == "" || !unicode.IsLower(r)
}

// osReadDirNames lists the file names in a directory.
func osReadDirNames(dir dirPath) ([]string, error) {
	entries, err := os.ReadDir(string(dir))
	if err != nil {
		return nil, err
	}
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	return names, nil
}

// osArch is a GOOS or GOARCH word within a file-name constraint suffix.
type osArch string

// The known GOOS and GOARCH values, for recognizing filename-implied build
// constraints. Go does not export these lists (they live in internal/syslist);
// this data is copied from src/internal/syslist/syslist.go — the same source
// go/build consults for goodOSArchFile.
var (
	knownOS = set(
		"aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos",
		"ios", "js", "linux", "nacl", "netbsd", "openbsd", "plan9", "solaris",
		"wasip1", "windows", "zos",
	)
	knownArch = set(
		"386", "amd64", "amd64p32", "arm", "armbe", "arm64", "arm64be",
		"loong64", "mips", "mipsle", "mips64", "mips64le", "mips64p32",
		"mips64p32le", "ppc", "ppc64", "ppc64le", "riscv", "riscv64", "s390",
		"s390x", "sparc", "sparc64", "wasm",
	)
)

// set builds a membership map from its arguments.
func set(names ...osArch) map[osArch]bool {
	m := make(map[osArch]bool, len(names))
	for _, name := range names {
		m[name] = true
	}
	return m
}
