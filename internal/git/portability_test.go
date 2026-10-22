package git

import "runtime"

// supportsUnusualFilenames reports whether the host filesystem can represent
// paths with characters such as ':' and newlines. Windows cannot. The parser
// tests still cover those paths as data; only tests that must create them on
// disk skip there.
func supportsUnusualFilenames() bool {
	return runtime.GOOS != "windows"
}
