//go:build darwin

package reviewtransaction

import "os"

func openReviewRepositoryContext(path string) (*os.File, error) {
	return os.Open(path)
}
