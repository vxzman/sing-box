//go:build !linux && !windows && !darwin && !freebsd

package process

import (
	"os"
)

func NewSearcher(_ Config) (Searcher, error) {
	return nil, os.ErrInvalid
}
