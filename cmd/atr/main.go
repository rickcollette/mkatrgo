// SPDX-License-Identifier: GPL-2.0-only

package main

import (
	"os"

	"github.com/megalith/mkatrgo/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
