//go:build !windows

package mux

import "os"

func isPlatformReparsePoint(os.FileInfo) bool {
	return false
}
