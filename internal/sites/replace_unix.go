//go:build !windows

package sites

import "os"

func replace(from, to string) error { return os.Rename(from, to) }
