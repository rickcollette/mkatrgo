// SPDX-License-Identifier: GPL-2.0-only

package mkatrgo

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	dos2VTOC       = 360
	dos2Directory  = 361
	dos2DirSectors = 8
	dos2MaxFiles   = 64
)

// buildDOS2 makes a flat DOS 2.0S/2.0D disk (no directories). DOS 2.5 and
// MyDOS are different formats ... do not quietly pretend they are this one.
func (b *Builder) buildDOS2(options Options) ([]byte, Result, error) {
	sectorSize, sectors := options.SectorSize, options.Sectors
	if sectorSize == 0 && sectors == 0 {
		sectorSize, sectors = 128, 720
	}
	if (sectorSize != 128 && sectorSize != 256) || sectors != 720 {
		return nil, Result{}, errors.New("Atari DOS 2 creation is 720 sectors (128 or 256 bytes each)")
	}
	if len(options.BootSectors) != 0 && len(options.BootSectors) != 384 {
		return nil, Result{}, errors.New("boot sectors must be exactly 384 bytes (3 x 128)")
	}
	files := make([]*entry, 0, len(b.entries)-1)
	for _, item := range b.entries {
		if item == b.root {
			continue
		}
		if item.directory || item.parent != b.root {
			return nil, Result{}, fmt.Errorf("Atari DOS 2 has no directories (found %q)", item.imagePath)
		}
		files = append(files, item)
	}
	if len(files) > dos2MaxFiles {
		return nil, Result{}, errors.New("Atari DOS 2 is full at 64 directory entries")
	}
	sort.SliceStable(files, func(i, j int) bool { return files[i].imagePath < files[j].imagePath })
	logical := make([]byte, sectors*sectorSize)
	if len(options.BootSectors) == 384 {
		for index := 0; index < 3; index++ {
			copy(logical[index*sectorSize:index*sectorSize+128], options.BootSectors[index*128:(index+1)*128])
		}
	}
	used := make([]bool, sectors+1)
	used[0] = true
	for sector := 1; sector <= 3; sector++ {
		used[sector] = true
	}
	used[dos2VTOC] = true
	for sector := dos2Directory; sector < dos2Directory+dos2DirSectors; sector++ {
		used[sector] = true
	}
	// single density's VTOC stops at sector 719 (sector 720 has no bit).
	if sectorSize == 128 {
		used[720] = true
	}
	nextFree := 4
	allocate := func() (int, error) {
		for nextFree <= sectors && used[nextFree] {
			nextFree++
		}
		if nextFree > sectors {
			return 0, errNoSpace
		}
		sector := nextFree
		used[sector] = true
		nextFree++
		return sector, nil
	}
	dataBytes := sectorSize - 3
	for fileNumber, item := range files {
		count := (len(item.data) + dataBytes - 1) / dataBytes
		if count == 0 {
			count = 1
		}
		chain := make([]int, count)
		for index := range chain {
			sector, err := allocate()
			if err != nil {
				return nil, Result{}, fmt.Errorf("add %s: %w", item.imagePath, err)
			}
			chain[index] = sector
		}
		for index, sector := range chain {
			block := logical[(sector-1)*sectorSize : sector*sectorSize]
			start := index * dataBytes
			end := start + dataBytes
			if end > len(item.data) {
				end = len(item.data)
			}
			copy(block, item.data[start:end])
			next := 0
			if index+1 < len(chain) {
				next = chain[index+1]
			}
			block[dataBytes] = byte(fileNumber<<2) | byte(next>>8)&3
			block[dataBytes+1] = byte(next)
			block[dataBytes+2] = byte(end - start)
		}
		sector := dos2Directory + fileNumber/8
		offset := (sector-1)*sectorSize + (fileNumber%8)*16
		row := logical[offset : offset+16]
		row[0] = 0x42
		if item.attributes&AttributeProtected != 0 {
			row[0] |= 0x20
		}
		binary.LittleEndian.PutUint16(row[1:3], uint16(len(chain)))
		binary.LittleEndian.PutUint16(row[3:5], uint16(chain[0]))
		copy(row[5:16], item.name[:])
	}
	allocatable := 0
	free := 0
	for sector := 1; sector <= sectors; sector++ {
		if sector <= 3 || sector == dos2VTOC || sector >= dos2Directory && sector < dos2Directory+dos2DirSectors || sectorSize == 128 && sector == 720 {
			continue
		}
		allocatable++
		if !used[sector] {
			free++
		}
	}
	vtoc := logical[(dos2VTOC-1)*sectorSize : dos2VTOC*sectorSize]
	vtoc[0] = 2
	binary.LittleEndian.PutUint16(vtoc[1:3], uint16(allocatable))
	binary.LittleEndian.PutUint16(vtoc[3:5], uint16(free))
	for sector := 0; sector <= sectors; sector++ {
		position := 10 + sector/8
		if !used[sector] && position < len(vtoc) {
			vtoc[position] |= 0x80 >> (sector & 7)
		}
	}
	image := writeATR(logical, sectorSize, sectors)
	return image, Result{SectorSize: sectorSize, Sectors: sectors, Bytes: len(image)}, nil
}

func looksDOS2(data []byte, sectorSize, sectors int) bool {
	if sectors < dos2Directory+dos2DirSectors-1 || sectorSize != 128 && sectorSize != 256 {
		return false
	}
	vtoc := data[(dos2VTOC-1)*sectorSize : dos2VTOC*sectorSize]
	code := vtoc[0]
	total := int(binary.LittleEndian.Uint16(vtoc[1:3]))
	free := int(binary.LittleEndian.Uint16(vtoc[3:5]))
	if code < 1 || code >= 16 || total <= 0 || total > sectors || free > total {
		return false
	}
	// sector zero + the filesystem sectors have to be allocated. this also
	// keeps random sector-360 data from looking like a VTOC (easy false match).
	if vtoc[10]&0x80 != 0 || vtoc[55]&0x80 != 0 {
		return false
	}
	if code == 2 && vtoc[10]&0xf0 != 0 {
		return false
	}
	return true
}

func parseDOS2(data []byte, sectorSize, sectors, bytes int) (*Image, error) {
	vtoc := data[(dos2VTOC-1)*sectorSize : dos2VTOC*sectorSize]
	entries, err := readDOS2Directory(data, sectorSize, sectors, dos2Directory, "", map[int]bool{})
	if err != nil {
		return nil, err
	}
	bootSectors := make([]byte, 0, 384)
	for index := 0; index < 3; index++ {
		bootSectors = append(bootSectors, data[index*sectorSize:index*sectorSize+128]...)
	}
	return &Image{
		Filesystem:  "dos2",
		Revision:    int(vtoc[0]),
		Volume:      "DOS 2",
		SectorSize:  sectorSize,
		Sectors:     sectors,
		Bytes:       bytes,
		FreeBytes:   int(binary.LittleEndian.Uint16(vtoc[3:5])) * sectorSize,
		Entries:     entries,
		bootSectors: bootSectors,
	}, nil
}

func readDOS2Directory(data []byte, sectorSize, sectors, base int, parent string, ancestors map[int]bool) ([]ImageEntry, error) {
	if base < 1 || base+7 > sectors || ancestors[base] {
		return nil, fmt.Errorf("invalid DOS 2 directory sector %d", base)
	}
	ancestors = cloneSet(ancestors)
	ancestors[base] = true
	entries := make([]ImageEntry, 0)
	for slot := 0; slot < dos2MaxFiles; slot++ {
		sector := base + slot/8
		block := data[(sector-1)*sectorSize : sector*sectorSize]
		row := block[(slot%8)*16 : (slot%8+1)*16]
		flags := row[0]
		if flags == 0 {
			break
		}
		if flags&0x80 != 0 {
			continue
		}
		name := displayNameBytes(row[5:16])
		if name == "" {
			return nil, fmt.Errorf("invalid DOS 2 filename in slot %d", slot)
		}
		path := name
		if parent != "" {
			path = parent + "/" + name
		}
		first := int(binary.LittleEndian.Uint16(row[3:5]))
		if flags&0x10 != 0 {
			children, err := readDOS2Directory(data, sectorSize, sectors, first, path, ancestors)
			if err != nil {
				return nil, err
			}
			entries = append(entries, ImageEntry{Path: path, Name: name, Directory: true, Size: int(binary.LittleEndian.Uint16(row[1:3])) * 128})
			entries = append(entries, children...)
			continue
		}
		if flags&0x41 == 0 {
			return nil, fmt.Errorf("invalid DOS 2 flags $%02X for %s", flags, path)
		}
		contents, err := readDOS2Chain(data, sectorSize, sectors, first, slot, flags&0x04 != 0)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		attributes := Attributes(0)
		if flags&0x20 != 0 {
			attributes |= AttributeProtected
		}
		entries = append(entries, ImageEntry{Path: path, Name: name, Size: len(contents), Attributes: attributes, Boot: strings.EqualFold(path, "DOS.SYS"), data: contents})
	}
	return entries, nil
}

func readDOS2Chain(data []byte, sectorSize, sectors, first, fileNumber int, longLinks bool) ([]byte, error) {
	result := make([]byte, 0)
	seen := make(map[int]bool)
	dataBytes := sectorSize - 3
	for sector := first; sector != 0; {
		if sector < 1 || sector > sectors || seen[sector] {
			return nil, fmt.Errorf("invalid or repeated data sector %d", sector)
		}
		seen[sector] = true
		block := data[(sector-1)*sectorSize : sector*sectorSize]
		count := int(block[dataBytes+2])
		if count > dataBytes {
			return nil, fmt.Errorf("sector %d has invalid byte count %d", sector, count)
		}
		result = append(result, block[:count]...)
		if longLinks {
			sector = int(block[dataBytes])<<8 | int(block[dataBytes+1])
		} else {
			if int(block[dataBytes]>>2) != fileNumber {
				return nil, fmt.Errorf("sector %d belongs to directory slot %d", sector, block[dataBytes]>>2)
			}
			sector = int(block[dataBytes]&3)<<8 | int(block[dataBytes+1])
		}
	}
	return result, nil
}
