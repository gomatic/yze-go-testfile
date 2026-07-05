package c_test

// aaa_test.go is exempt (only an Example) and reports nothing. It sorts first
// in the pass, which under the old directory-scan model wrongly made it the
// anchor for zzz_test.go's diagnostic; each file now anchors its own report.
func ExampleAaa() {}
