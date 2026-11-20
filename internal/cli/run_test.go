// SPDX-License-Identifier: GPL-2.0-only

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateListPutAndGet(t *testing.T) {
	root := t.TempDir()
	one := filepath.Join(root, "one.txt")
	two := filepath.Join(root, "two.bin")
	image := filepath.Join(root, "disk.atr")
	if err := os.WriteFile(one, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(two, []byte{2, 2}, 0o644); err != nil {
		t.Fatal(err)
	}
	var output, errors bytes.Buffer
	if code := Run([]string{"create", image, one}, &output, &errors); code != 0 {
		t.Fatalf("create failed (%d): %s", code, errors.String())
	}
	output.Reset()
	errors.Reset()
	if code := Run([]string{"put", "--to", "DATA", image, two}, &output, &errors); code != 0 {
		t.Fatalf("put failed (%d): %s", code, errors.String())
	}
	output.Reset()
	errors.Reset()
	if code := Run([]string{"list", image}, &output, &errors); code != 0 {
		t.Fatalf("list failed (%d): %s", code, errors.String())
	}
	if !strings.Contains(output.String(), "ONE.TXT") || !strings.Contains(output.String(), "DATA/TWO.BIN") {
		t.Fatalf("unexpected list:\n%s", output.String())
	}
	extracted := filepath.Join(root, "copy.bin")
	if code := Run([]string{"get", image, "DATA/TWO.BIN", extracted}, &output, &errors); code != 0 {
		t.Fatalf("get failed (%d): %s", code, errors.String())
	}
	got, err := os.ReadFile(extracted)
	if err != nil || !bytes.Equal(got, []byte{2, 2}) {
		t.Fatalf("bad extracted file: % x, %v", got, err)
	}
}
