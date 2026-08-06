package lsp

import (
	"io"
	"os"
)

// openFileNative wraps os.Open so client.go can keep its variable
// seam (`openReader`) typed against an interface that's mockable
// without pulling os into every test.
func openFileNative(p string) (io.ReadCloser, error) {
	return os.Open(p)
}
