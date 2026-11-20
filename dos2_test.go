// SPDX-License-Identifier: GPL-2.0-only

package mkatrgo

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDOS2BuildParseAndRewrite(t *testing.T) {
	want := bytes.Repeat([]byte("DOS2"), 100)
	builder := NewBuilder()
	if err := builder.AddData("HELLO.COM", want, AttributeProtected, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	boot := make([]byte, 384)
	boot[1] = 3
	boot[6], boot[7], boot[8] = 0x4c, 0x00, 0x07
	raw, result, err := builder.Build(Options{Filesystem: "dos2", SectorSize: 128, Sectors: 720, BootSectors: boot})
	if err != nil {
		t.Fatal(err)
	}
	if result.Sectors != 720 || result.SectorSize != 128 {
		t.Fatalf("unexpected geometry: %+v", result)
	}
	vtoc := raw[16+(dos2VTOC-1)*128:]
	if vtoc[0] != 2 || binary.LittleEndian.Uint16(vtoc[1:3]) != 707 {
		t.Fatalf("unexpected VTOC: % x", vtoc[:5])
	}
	image, err := ParseImage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if image.Filesystem != "dos2" || len(image.Entries) != 1 || !bytes.Equal(image.Entries[0].data, want) {
		t.Fatalf("bad DOS 2 round trip: %+v", image)
	}
	if image.Entries[0].Attributes != AttributeProtected {
		t.Fatal("protected flag was not retained")
	}
	path := filepath.Join(t.TempDir(), "disk.atr")
	if err := image.Save(path); err != nil {
		t.Fatal(err)
	}
	rewritten, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rewritten[16:16+9], boot[:9]) {
		t.Fatal("DOS 2 boot sectors changed during rewrite")
	}
}

func TestDOS2RejectsDirectories(t *testing.T) {
	builder := NewBuilder()
	if err := builder.AddDirectory("SUB", 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := builder.Build(Options{Filesystem: "dos2"}); err == nil {
		t.Fatal("DOS 2 accepted a subdirectory")
	}
}

func TestEOSNativeBootLoader(t *testing.T) {
	builder := NewBuilder()
	if err := builder.AddData("EOS.SYS", []byte{0xff, 0xff, 0, 0x20, 0, 0x20, 0x60}, 0, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	loader := EOSBootSectors()
	raw, _, err := builder.Build(Options{Filesystem: "eos", Revision: 0x45, BootSectors: loader})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw[16:16+9], loader[:9]) {
		t.Fatalf("EOS loader header changed: got % x want % x", raw[16:25], loader[:9])
	}
	image, err := ParseImage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(image.Entries) != 1 || !image.Entries[0].Boot {
		t.Fatalf("EOS boot file was not marked: %+v", image.Entries)
	}
}

func TestLoadBootSectorsFromATR(t *testing.T) {
	builder := NewBuilder()
	boot := make([]byte, 384)
	boot[1] = 3
	path := filepath.Join(t.TempDir(), "master.atr")
	if _, err := builder.Create(path, Options{BootSectors: boot}); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadBootSectors(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 384 || !bytes.Equal(loaded[:9], boot[:9]) {
		t.Fatal("ATR boot sectors were not loaded")
	}
}
