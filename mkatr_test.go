// SPDX-License-Identifier: GPL-2.0-only
package mkatrgo

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildBootableATR(t *testing.T) {
	root := t.TempDir()
	xex := filepath.Join(root, "hello.xex")
	want := []byte{0xff, 0xff, 0x00, 0x20, 0x03, 0x20, 1, 2, 3, 4}
	if err := os.WriteFile(xex, want, 0o644); err != nil {
		t.Fatal(err)
	}
	builder := NewBuilder()
	if err := builder.AddFile(xex, "HELLO.XEX", AttributeProtected, true); err != nil {
		t.Fatal(err)
	}
	image, result, err := builder.Build(Options{BootPage: 7})
	if err != nil {
		t.Fatal(err)
	}
	if result.SectorSize != 128 || result.Sectors != 720 {
		t.Fatalf("unexpected geometry: %+v", result)
	}
	if image[0] != 0x96 || image[1] != 0x02 || binary.LittleEndian.Uint16(image[4:6]) != 128 {
		t.Fatal("invalid ATR header")
	}
	payload := image[16:]
	bootMap := int(binary.LittleEndian.Uint16(payload[40:42]))
	if bootMap == 0 {
		t.Fatal("boot file map was not recorded")
	}
	mapData := sector128(payload, bootMap)
	dataSector := int(binary.LittleEndian.Uint16(mapData[4:6]))
	if dataSector == 0 || !bytes.Equal(sector128(payload, dataSector)[:len(want)], want) {
		t.Fatal("compiled application was not copied into the ATR")
	}
	mainMap := int(binary.LittleEndian.Uint16(payload[9:11]))
	mainDataSector := int(binary.LittleEndian.Uint16(sector128(payload, mainMap)[4:6]))
	row := sector128(payload, mainDataSector)[23:46]
	if string(row[6:17]) != "HELLO   XEX" || row[0]&byte(AttributeProtected) == 0 {
		t.Fatalf("wrong directory entry: %q flags=%02x", row[6:17], row[0])
	}
}

func TestBuildSelectsMinimumGeometry(t *testing.T) {
	file := filepath.Join(t.TempDir(), "data.bin")
	if err := os.WriteFile(file, bytes.Repeat([]byte{0x5a}, 200_000), 0o644); err != nil {
		t.Fatal(err)
	}
	builder := NewBuilder()
	if err := builder.AddFile(file, "DATA.BIN", 0, false); err != nil {
		t.Fatal(err)
	}
	_, result, err := builder.Build(Options{BootPage: 7})
	if err != nil {
		t.Fatal(err)
	}
	if result.SectorSize != 256 || result.Sectors != 1440 {
		t.Fatalf("unexpected geometry for 200 KB input: %+v", result)
	}
}

func TestRejectsDuplicateAtariNames(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "abcdefgh1.xex"), filepath.Join(root, "abcdefgh2.xex")
	_ = os.WriteFile(a, []byte{1}, 0o644)
	_ = os.WriteFile(b, []byte{2}, 0o644)
	builder := NewBuilder()
	if err := builder.AddFile(a, filepath.Base(a), 0, false); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddFile(b, filepath.Base(b), 0, false); err == nil {
		t.Fatal("duplicate 8.3 name was accepted")
	}
}

func TestBuildEOSFSWithExtentAndNameIndex(t *testing.T) {
	file := filepath.Join(t.TempDir(), "demo.xex")
	want := bytes.Repeat([]byte{0x3c}, 300)
	if err := os.WriteFile(file, want, 0o644); err != nil {
		t.Fatal(err)
	}
	builder := NewBuilder()
	if err := builder.AddFile(file, "APPS/DEMO.XEX", 0, false); err != nil {
		t.Fatal(err)
	}
	image, _, err := builder.Build(Options{Filesystem: "eos", BootPage: 7})
	if err != nil {
		t.Fatal(err)
	}
	payload := image[16:]
	if payload[32] != 0x45 || binary.LittleEndian.Uint16(payload[33:35]) != 128 {
		t.Fatalf("invalid EOSFS header: revision=%02x sector=%d", payload[32], binary.LittleEndian.Uint16(payload[33:35]))
	}
	mainMap := int(binary.LittleEndian.Uint16(payload[9:11]))
	mainList := sector128(payload, mainMap)
	mainSector := int(binary.LittleEndian.Uint16(mainList[4:6]))
	main := sector128(payload, mainSector)
	if main[22] != 0xff {
		t.Fatal("MAIN directory has no EOSFS name index")
	}
	indexMap := int(binary.LittleEndian.Uint16(main[20:22]))
	indexList := sector128(payload, indexMap)
	indexSector := int(binary.LittleEndian.Uint16(indexList[4:6]))
	index := sector128(payload, indexSector)
	if index[1] != nameHash(main[23+6:23+17]) {
		t.Fatalf("wrong EOSFS name hash: got %02x", index[1])
	}
	appsMap := int(binary.LittleEndian.Uint16(main[23+1 : 23+3]))
	appsList := sector128(payload, appsMap)
	appsSector := int(binary.LittleEndian.Uint16(appsList[4:6]))
	apps := sector128(payload, appsSector)
	fileMap := int(binary.LittleEndian.Uint16(apps[23+1 : 23+3]))
	fileList := sector128(payload, fileMap)
	start := int(binary.LittleEndian.Uint16(fileList[4:6]))
	count := int(binary.LittleEndian.Uint16(fileList[6:8]))
	if count != 3 {
		t.Fatalf("expected one 3-sector extent, got start=%d count=%d", start, count)
	}
	got := append(append([]byte{}, sector128(payload, start)...), sector128(payload, start+1)...)
	got = append(got, sector128(payload, start+2)...)
	if !bytes.Equal(got[:len(want)], want) {
		t.Fatal("EOSFS file extent does not contain the application")
	}
}

func TestEOSFSRejectsSDFSBootFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "demo.xex")
	if err := os.WriteFile(file, []byte{1}, 0o644); err != nil {
		t.Fatal(err)
	}
	builder := NewBuilder()
	if err := builder.AddFile(file, "DEMO.XEX", 0, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := builder.Build(Options{Filesystem: "eos", BootPage: 7}); err == nil {
		t.Fatal("EOSFS accepted an incompatible SDFS boot file")
	}
}

func TestCreateReplacesExistingImage(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "demo.xex")
	output := filepath.Join(root, "demo.atr")
	if err := os.WriteFile(file, []byte{1, 2, 3}, 0o644); err != nil {
		t.Fatal(err)
	}
	builder := NewBuilder()
	if err := builder.AddFile(file, "DEMO.XEX", 0, false); err != nil {
		t.Fatal(err)
	}
	if _, err := builder.Create(output, Options{BootPage: 7}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte{4, 5, 6, 7}, 0o644); err != nil {
		t.Fatal(err)
	}
	builder = NewBuilder()
	if err := builder.AddFile(file, "DEMO.XEX", 0, false); err != nil {
		t.Fatal(err)
	}
	if _, err := builder.Create(output, Options{BootPage: 7}); err != nil {
		t.Fatalf("second build could not replace the ATR: %v", err)
	}
	if info, err := os.Stat(output); err != nil || info.Size() <= 16 {
		t.Fatalf("replacement image is unavailable: size=%v err=%v", info, err)
	}
}

func sector128(payload []byte, number int) []byte {
	start := (number - 1) * 128
	return payload[start : start+128]
}
