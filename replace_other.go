// SPDX-License-Identifier: GPL-2.0-only
//go:build !windows

package mkatrgo

import "os"

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}
