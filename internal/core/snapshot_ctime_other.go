//go:build !linux

package core

import "os"

func fileChangeTime(_ os.FileInfo) (int64, bool) {
	return 0, false
}
