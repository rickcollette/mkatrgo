// SPDX-License-Identifier: GPL-2.0-only
package mkatrgo

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestImageExplorerRoundTrip(t *testing.T) {
	for _, filesystem := range []string{"sparta", "eos"} {
		t.Run(filesystem, func(t *testing.T) {
			root := t.TempDir()
			original := filepath.Join(root, "demo.xex")
			if err := os.WriteFile(original, []byte{1, 2, 3, 4}, 0o644); err != nil {
				t.Fatal(err)
			}
			builder := NewBuilder()
			if err := builder.AddFile(original, "APPS/DEMO.XEX", 0, filesystem == "sparta"); err != nil {
				t.Fatal(err)
			}
			imagePath := filepath.Join(root, filesystem+".atr")
			if _, err := builder.Create(imagePath, Options{Filesystem: filesystem, BootPage: 7}); err != nil {
				t.Fatal(err)
			}

			image, err := OpenImage(imagePath)
			if err != nil {
				t.Fatal(err)
			}
			if image.Filesystem != filesystem || image.SectorSize != 128 || image.Sectors != 720 {
				t.Fatalf("unexpected catalog: %+v", image)
			}
			if len(image.Entries) != 2 || image.Entries[0].Path != "APPS" || image.Entries[1].Path != "APPS/DEMO.XEX" {
				t.Fatalf("unexpected entries: %+v", image.Entries)
			}

			added := filepath.Join(root, "notes.txt")
			want := bytes.Repeat([]byte("ATR explorer\n"), 20)
			if err := os.WriteFile(added, want, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := image.Put([]string{added}, "DOCS"); err != nil {
				t.Fatal(err)
			}
			if err := image.Mkdir("EMPTY"); err != nil {
				t.Fatal(err)
			}
			if err := image.Rename("APPS/DEMO.XEX", "APPS/START.XEX"); err != nil {
				t.Fatal(err)
			}
			if err := image.Remove("EMPTY"); err != nil {
				t.Fatal(err)
			}
			if err := image.Save(imagePath); err != nil {
				t.Fatal(err)
			}

			reopened, err := OpenImage(imagePath)
			if err != nil {
				t.Fatal(err)
			}
			if reopened.SectorSize != 128 || reopened.Sectors != 720 {
				t.Fatalf("geometry changed: %d x %d", reopened.Sectors, reopened.SectorSize)
			}
			extracted := filepath.Join(root, "extracted.txt")
			if err := reopened.Extract("DOCS/NOTES.TXT", extracted); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(extracted)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("extracted data mismatch: %v", err)
			}
		})
	}
}

func TestAddDataPreservesTimestamp(t *testing.T) {
	builder := NewBuilder()
	modified := time.Date(2025, 6, 7, 8, 9, 10, 0, time.Local)
	if err := builder.AddData("STAMP.DAT", []byte{1}, AttributeArchived, false, modified); err != nil {
		t.Fatal(err)
	}
	image, _, err := builder.Build(Options{BootPage: 7})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseImage(image)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Entries) != 1 || !parsed.Entries[0].Modified.Equal(modified) || parsed.Entries[0].Attributes != AttributeArchived {
		t.Fatalf("metadata was not preserved: %+v", parsed.Entries)
	}
}
