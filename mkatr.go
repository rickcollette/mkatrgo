// SPDX-License-Identifier: GPL-2.0-only
//
// Go port of Daniel Serpell's mkatr ... https://github.com/dmsc/mkatr
package mkatrgo

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	AttributeProtected Attributes = 1
	AttributeHidden    Attributes = 2
	AttributeArchived  Attributes = 4
	maxDirectoryBytes             = 32768
	maxFileBytes                  = 0x1000000
)

var errNoSpace = errors.New("image is out of space")

// Attributes are the protected/hidden/archived flags on an entry.
type Attributes byte

// Options is the image geometry + filesystem/boot setup.
type Options struct {
	Filesystem  string
	Revision    int
	Volume      string
	ExactSize   bool
	MinimumSize int
	BootPage    int
	SectorSize  int
	Sectors     int
	// BootSectors can replace the three 128-byte boot sectors. filesystem
	// header fields get filled in after the copy (so they still match the disk).
	BootSectors []byte
}

// Result is the geometry/size that actually got built.
type Result struct {
	SectorSize int
	Sectors    int
	Bytes      int
}

type entry struct {
	hostPath   string
	imagePath  string
	name       [11]byte
	data       []byte
	size       int
	parent     *entry
	level      int
	directory  bool
	attributes Attributes
	boot       bool
	mapSector  int
	date       [3]byte
	clock      [3]byte
}

// Builder collects everything that will go into a new image.
type Builder struct {
	entries        []*entry
	root           *entry
	byImagePath    map[string]*entry
	cliDirectories []*entry
}

// NewBuilder starts with an empty MAIN directory.
func NewBuilder() *Builder {
	now := time.Now()
	root := &entry{imagePath: "", directory: true, size: 23}
	root.name = atariName("MAIN")
	setTimestamp(root, now)
	b := &Builder{root: root, byImagePath: map[string]*entry{"": root}}
	b.entries = append(b.entries, root)
	b.cliDirectories = append(b.cliDirectories, root)
	return b
}

// AddFile adds one host file (missing parent directories are made as needed).
// image paths use forward slashes.
func (b *Builder) AddFile(hostPath, imagePath string, attributes Attributes, boot bool) error {
	info, err := os.Stat(hostPath)
	if err != nil {
		return fmt.Errorf("read input %q: %w", hostPath, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("input %q is not a regular file", hostPath)
	}
	if info.Size() > maxFileBytes {
		return fmt.Errorf("input %q exceeds 16 MiB", hostPath)
	}
	clean, err := cleanImagePath(imagePath)
	if err != nil {
		return err
	}
	if clean == "" {
		clean = filepath.Base(hostPath)
	}
	data, err := os.ReadFile(hostPath)
	if err != nil {
		return fmt.Errorf("read input %q: %w", hostPath, err)
	}
	return b.addData(hostPath, clean, data, attributes, boot, info.ModTime())
}

// AddData is AddFile for bytes already in memory.
func (b *Builder) AddData(imagePath string, data []byte, attributes Attributes, boot bool, modified time.Time) error {
	clean, err := cleanImagePath(imagePath)
	if err != nil || clean == "" {
		if err == nil {
			err = errors.New("ATR file path is empty")
		}
		return err
	}
	if len(data) > maxFileBytes {
		return fmt.Errorf("input %q exceeds 16 MiB", imagePath)
	}
	return b.addData("", clean, append([]byte(nil), data...), attributes, boot, modified)
}

func (b *Builder) addData(hostPath, clean string, data []byte, attributes Attributes, boot bool, modified time.Time) error {
	parent, err := b.ensureDirectories(filepath.ToSlash(filepath.Dir(clean)), modified)
	if err != nil {
		return err
	}
	return b.addEntry(&entry{hostPath: hostPath, imagePath: clean, data: data, size: len(data), parent: parent, level: parent.level + 1, attributes: attributes, boot: boot}, modified)
}

// AddDirectory adds a directory (including an empty one).
func (b *Builder) AddDirectory(imagePath string, attributes Attributes, modified time.Time) error {
	clean, err := cleanImagePath(imagePath)
	if err != nil || clean == "" {
		if err == nil {
			err = errors.New("ATR directory path is empty")
		}
		return err
	}
	parent, err := b.ensureDirectories(filepath.ToSlash(filepath.Dir(clean)), modified)
	if err != nil {
		return err
	}
	if existing := b.byImagePath[strings.ToUpper(clean)]; existing != nil {
		if existing.directory {
			return nil
		}
		return fmt.Errorf("ATR path %q is already a file", clean)
	}
	return b.addEntry(&entry{imagePath: clean, parent: parent, level: parent.level + 1, directory: true, size: 23, attributes: attributes}, modified)
}

// AddCLIPath keeps old mkatr argument behavior: mention a directory first,
// then later files below that host path land inside it (everything else is in
// MAIN).
func (b *Builder) AddCLIPath(hostPath string, attributes Attributes, boot bool) error {
	info, err := os.Stat(hostPath)
	if err != nil {
		return fmt.Errorf("read input %q: %w", hostPath, err)
	}
	if !info.Mode().IsRegular() && !info.IsDir() {
		return fmt.Errorf("invalid input type %q", hostPath)
	}
	abs, err := filepath.Abs(hostPath)
	if err != nil {
		return err
	}
	parent := b.root
	parentHostLen := -1
	for _, candidate := range b.cliDirectories {
		if candidate == b.root {
			continue
		}
		if pathWithin(candidate.hostPath, abs) && len(candidate.hostPath) > parentHostLen {
			parent, parentHostLen = candidate, len(candidate.hostPath)
		}
	}
	imagePath := filepath.Base(abs)
	if parent != b.root {
		imagePath = parent.imagePath + "/" + imagePath
	}
	e := &entry{hostPath: abs, imagePath: filepath.ToSlash(imagePath), parent: parent, level: parent.level + 1, attributes: attributes}
	if info.IsDir() {
		e.directory, e.size = true, 23
	} else {
		if info.Size() > maxFileBytes {
			return fmt.Errorf("input %q exceeds 16 MiB", hostPath)
		}
		e.data, err = os.ReadFile(abs)
		if err != nil {
			return err
		}
		e.size, e.boot = len(e.data), boot
	}
	if err := b.addEntry(e, info.ModTime()); err != nil {
		return err
	}
	if e.directory {
		b.cliDirectories = append(b.cliDirectories, e)
	}
	return nil
}

func pathWithin(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func cleanImagePath(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "\\", "/"), ">", "/"))
	value = strings.TrimPrefix(value, "./")
	value = strings.Trim(value, "/")
	if value == "" {
		return "", nil
	}
	parts := strings.Split(value, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("invalid ATR path %q", value)
		}
	}
	return strings.Join(parts, "/"), nil
}

func (b *Builder) ensureDirectories(dir string, modTime time.Time) (*entry, error) {
	if dir == "." || dir == "" {
		return b.root, nil
	}
	parent := b.root
	current := ""
	for _, part := range strings.Split(dir, "/") {
		if current == "" {
			current = part
		} else {
			current += "/" + part
		}
		if existing := b.byImagePath[strings.ToUpper(current)]; existing != nil {
			if !existing.directory {
				return nil, fmt.Errorf("ATR path %q is already a file", current)
			}
			parent = existing
			continue
		}
		e := &entry{imagePath: current, parent: parent, level: parent.level + 1, directory: true, size: 23}
		if err := b.addEntry(e, modTime); err != nil {
			return nil, err
		}
		parent = e
	}
	return parent, nil
}

func (b *Builder) addEntry(e *entry, modTime time.Time) error {
	e.name = atariName(filepath.Base(e.imagePath))
	if string(e.name[:]) == "           " {
		return fmt.Errorf("cannot add empty Atari filename for %q", e.imagePath)
	}
	for _, other := range b.entries {
		if other.parent == e.parent && other.name == e.name {
			return fmt.Errorf("duplicate Atari 8.3 name %q", displayName(e.name))
		}
	}
	key := strings.ToUpper(e.imagePath)
	if b.byImagePath[key] != nil {
		return fmt.Errorf("duplicate ATR path %q", e.imagePath)
	}
	setTimestamp(e, modTime)
	b.entries = append(b.entries, e)
	b.byImagePath[key] = e
	return nil
}

func setTimestamp(e *entry, value time.Time) {
	e.date = [3]byte{byte(value.Day()), byte(value.Month()), byte(value.Year() % 100)}
	e.clock = [3]byte{byte(value.Hour()), byte(value.Minute()), byte(value.Second())}
}

func atariName(value string) [11]byte {
	var out [11]byte
	for i := range out {
		out[i] = ' '
	}
	base := filepath.Base(strings.TrimRight(value, `/\\`))
	name, ext, hasDot := base, "", false
	if dot := strings.IndexByte(base, '.'); dot >= 0 {
		name, ext, hasDot = base[:dot], base[dot+1:], true
	}
	copyPart := func(dst []byte, src string) {
		for i := 0; i < len(dst) && i < len(src); i++ {
			c := src[i]
			switch {
			case c >= 'a' && c <= 'z':
				dst[i] = c - 'a' + 'A'
			case c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
				dst[i] = c
			default:
				dst[i] = '_'
			}
		}
	}
	copyPart(out[:8], name)
	if hasDot {
		copyPart(out[8:], ext)
	}
	return out
}

func displayName(name [11]byte) string {
	base := strings.TrimRight(string(name[:8]), " ")
	ext := strings.TrimRight(string(name[8:]), " ")
	if ext != "" {
		return base + "." + ext
	}
	return base
}

// Create builds and atomically writes an ATR.
func (b *Builder) Create(output string, options Options) (Result, error) {
	image, result, err := b.Build(options)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return Result{}, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(output), ".mkatr-*.tmp")
	if err != nil {
		return Result{}, err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(image); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return Result{}, err
	}
	if err = replaceFile(name, output); err != nil {
		return Result{}, err
	}
	return result, nil
}

// Build returns the complete ATR file (header and all).
func (b *Builder) Build(options Options) ([]byte, Result, error) {
	filesystem := strings.ToLower(strings.TrimSpace(options.Filesystem))
	if filesystem == "dos2" || filesystem == "atari-dos2" || filesystem == "ataridos" {
		return b.buildDOS2(options)
	}
	if filesystem == "" || filesystem == "sdfs" || filesystem == "spartados" {
		filesystem = "sparta"
	}
	if filesystem != "sparta" && filesystem != "eos" && filesystem != "eosfs" {
		return nil, Result{}, fmt.Errorf("unsupported filesystem %q", options.Filesystem)
	}
	eosfs := filesystem == "eos" || filesystem == "eosfs"
	if eosfs {
		for _, item := range b.entries {
			if item.boot && len(options.BootSectors) != 384 {
				return nil, Result{}, errors.New("an EOSFS boot file requires compatible 384-byte EOS boot sectors")
			}
		}
	}
	if len(options.BootSectors) != 0 && len(options.BootSectors) != 384 {
		return nil, Result{}, errors.New("boot sectors must be exactly 384 bytes (3 x 128)")
	}
	if options.Revision == 0 {
		if eosfs {
			options.Revision = 0x45
		} else {
			options.Revision = 0x20
		}
	}
	if (!eosfs && options.Revision != 0x20 && options.Revision != 0x21) ||
		(eosfs && options.Revision != 0x45 && options.Revision != 0x46) {
		return nil, Result{}, fmt.Errorf("revision $%02X is not valid for %s", options.Revision, filesystem)
	}
	if options.BootPage == 0 {
		options.BootPage = 7
	}
	if options.BootPage <= 3 || options.BootPage >= 0xf0 {
		return nil, Result{}, errors.New("boot page must be 4..239")
	}
	maxSize := imageSize(65535, 256)
	if options.MinimumSize < 0 || options.MinimumSize > maxSize {
		return nil, Result{}, fmt.Errorf("minimum image size must be from 0 to %d", maxSize)
	}
	var fs *spartaFS
	if options.SectorSize != 0 || options.Sectors != 0 {
		if (options.SectorSize != 128 && options.SectorSize != 256) || options.Sectors < 6 || options.Sectors > 65535 {
			return nil, Result{}, errors.New("explicit geometry needs 128/256-byte sectors and 6..65535 sectors")
		}
		fs, _ = b.buildSparta(options.SectorSize, options.Sectors, options.BootPage, eosfs, options)
	} else if options.ExactSize {
		if options.MinimumSize <= imageSize(65535, 128) {
			fs, _ = b.buildSparta(128, 65535, options.BootPage, eosfs, options)
		}
		if fs == nil {
			fs, _ = b.buildSparta(256, 65535, options.BootPage, eosfs, options)
		}
		if fs != nil {
			nsec, ssec := 65535-fs.freeSectors(), fs.sectorSize
			if imageSize(nsec, ssec) < options.MinimumSize {
				nsec = imageSectors(options.MinimumSize, ssec)
			}
			for ; nsec > 5 && imageSize(nsec, ssec) >= options.MinimumSize; nsec-- {
				next, err := b.buildSparta(ssec, nsec, options.BootPage, eosfs, options)
				if err != nil {
					break
				}
				fs = next
				if fs.freeSectors() > 0 && imageSize(nsec-1, ssec) > options.MinimumSize {
					nsec = nsec - fs.freeSectors() + 1
					if imageSize(nsec, ssec) < options.MinimumSize {
						nsec = 1 + imageSectors(options.MinimumSize, ssec)
					}
				}
			}
		}
	} else {
		for _, size := range standardSizes {
			if imageSize(size.sectors, size.sectorSize) < options.MinimumSize {
				continue
			}
			candidate, err := b.buildSparta(size.sectorSize, size.sectors, options.BootPage, eosfs, options)
			if err == nil {
				fs = candidate
				break
			}
		}
	}
	if fs == nil {
		return nil, Result{}, errors.New("nothing fits ... cannot make an ATR large enough")
	}
	image := writeATR(fs.data, fs.sectorSize, fs.sectors)
	return image, Result{SectorSize: fs.sectorSize, Sectors: fs.sectors, Bytes: len(image)}, nil
}

type diskSize struct{ sectorSize, sectors int }

var standardSizes = []diskSize{{128, 720}, {128, 1040}, {256, 720}, {256, 1440}, {256, 2048}, {256, 4096}, {256, 8192}, {256, 16384}, {256, 32768}, {256, 65535}}

func imageSize(sectors, sectorSize int) int {
	if sectorSize == 256 {
		if sectors < 3 {
			return sectors * 128
		}
		return sectors*sectorSize - 3*128
	}
	return sectors * sectorSize
}

func imageSectors(size, sectorSize int) int {
	if sectorSize == 256 {
		if size < 3*128 {
			return (size + 127) / 128
		}
		return (size + 3*128 + 255) / 256
	}
	return (size + sectorSize - 1) / sectorSize
}

func writeATR(data []byte, sectorSize, sectors int) []byte {
	payloadSize := imageSize(sectors, sectorSize)
	out := make([]byte, 16, 16+payloadSize)
	out[0], out[1] = 0x96, 0x02
	out[2], out[3], out[6] = byte(payloadSize>>4), byte(payloadSize>>12), byte(payloadSize>>20)
	binary.LittleEndian.PutUint16(out[4:6], uint16(sectorSize))
	for i := 0; i < sectors; i++ {
		length := sectorSize
		if i < 3 {
			length = 128
		}
		out = append(out, data[i*sectorSize:i*sectorSize+length]...)
	}
	return out
}

type spartaFS struct {
	data                                                       []byte
	sectors, bitmap, bitmapCount, current, bootMap, sectorSize int
	eosfs                                                      bool
}

func (b *Builder) buildSparta(sectorSize, sectors, bootPage int, eosfs bool, options Options) (*spartaFS, error) {
	fs := &spartaFS{data: make([]byte, sectorSize*sectors), sectors: sectors, bitmap: 4, sectorSize: sectorSize, eosfs: eosfs}
	fs.bitmapCount = ((sectors+8)/8 + sectorSize - 1) / sectorSize
	fs.current = 4 + fs.bitmapCount
	fs.writeBoot(bootPage, options.BootSectors)
	for i := fs.current; i <= fs.sectors; i++ {
		fs.freeSector(i)
	}
	entries := append([]*entry(nil), b.entries...)
	sort.Slice(entries, func(i, j int) bool {
		a, z := entries[i], entries[j]
		if a.level != z.level {
			return a.level > z.level
		}
		if a.directory != z.directory {
			return a.directory
		}
		return string(a.name[:]) < string(z.name[:])
	})
	for _, e := range entries {
		e.mapSector = 0
		if e.directory {
			e.size, e.data = 23, make([]byte, maxDirectoryBytes)
		}
	}
	mainSector := -1
	for _, e := range entries {
		if e.directory {
			e.data[0], e.data[3], e.data[4], e.data[5] = 0x28, byte(e.size), byte(e.size>>8), byte(e.size>>16)
			copy(e.data[6:17], e.name[:])
			copy(e.data[17:20], e.date[:])
			copy(e.data[20:23], e.clock[:])
			if eosfs {
				index := make([]byte, e.size/23)
				for slot, offset := 1, 23; offset+23 <= e.size; slot, offset = slot+1, offset+23 {
					row := e.data[offset : offset+23]
					if row[0]&0x08 != 0 && row[0]&0x10 == 0 {
						index[slot] = nameHash(row[6:17])
					}
				}
				indexMap, err := fs.addExtentData(index)
				if err != nil {
					return nil, err
				}
				binary.LittleEndian.PutUint16(e.data[20:22], uint16(indexMap))
				e.data[22] = 0xff
			}
		}
		var mapSector int
		var err error
		if eosfs {
			mapSector, err = fs.addExtentData(e.data[:e.size])
		} else {
			mapSector, err = fs.addData(e.data[:e.size])
		}
		if err != nil {
			return nil, err
		}
		e.mapSector = mapSector
		if e.parent != nil {
			if e.parent.size+23 > maxDirectoryBytes {
				return nil, fmt.Errorf("too many files in directory %s", e.parent.imagePath)
			}
			row := e.parent.data[e.parent.size : e.parent.size+23]
			row[0] = 0x08 | byte(e.attributes)
			if e.directory {
				row[0] |= 0x20
			}
			binary.LittleEndian.PutUint16(row[1:3], uint16(mapSector))
			row[3], row[4], row[5] = byte(e.size), byte(e.size>>8), byte(e.size>>16)
			copy(row[6:17], e.name[:])
			copy(row[17:20], e.date[:])
			copy(row[20:23], e.clock[:])
			e.parent.size += 23
		} else {
			mainSector = mapSector
		}
		if e.boot {
			fs.bootMap = mapSector
		}
	}
	for _, e := range entries {
		if e.directory && e.parent != nil {
			if err := fs.patchByte(e.mapSector, 1, byte(e.parent.mapSector)); err != nil {
				return nil, err
			}
			if err := fs.patchByte(e.mapSector, 2, byte(e.parent.mapSector>>8)); err != nil {
				return nil, err
			}
		}
	}
	if mainSector < 0 {
		return nil, errors.New("internal error: no MAIN directory")
	}
	crc := crc32.ChecksumIEEE(fs.data)
	if len(options.BootSectors) == 0 {
		fs.data[1], fs.data[7] = 0x03, 0x80
	}
	binary.LittleEndian.PutUint16(fs.data[9:11], uint16(mainSector))
	binary.LittleEndian.PutUint16(fs.data[11:13], uint16(fs.sectors))
	binary.LittleEndian.PutUint16(fs.data[13:15], uint16(fs.sectors-fs.current+1))
	fs.data[15] = byte(fs.bitmapCount)
	binary.LittleEndian.PutUint16(fs.data[16:18], uint16(fs.bitmap))
	binary.LittleEndian.PutUint16(fs.data[18:20], uint16(fs.current))
	binary.LittleEndian.PutUint16(fs.data[20:22], uint16(fs.current))
	copy(fs.data[22:26], "DSK_")
	fs.data[26], fs.data[27], fs.data[28], fs.data[29] = hex(crc>>8), hex(crc>>12), hex(crc>>16), hex(crc>>20)
	if volume := volumeName(options.Volume); volume != "" {
		copy(fs.data[22:30], volume)
	}
	fs.data[30] = 0x28
	if fs.sectorSize == 128 {
		fs.data[31] = 128
	}
	fs.data[32], fs.data[39] = byte(options.Revision), byte(crc)
	if options.Revision == 0x21 {
		binary.LittleEndian.PutUint16(fs.data[33:35], uint16(fs.sectorSize))
		binary.LittleEndian.PutUint16(fs.data[35:37], uint16((fs.sectorSize-4)/2))
		fs.data[37] = 1
	}
	if eosfs {
		if strings.TrimSpace(options.Volume) == "" {
			copy(fs.data[22:30], "EOS     ")
		}
		fs.data[32] = byte(options.Revision)
		binary.LittleEndian.PutUint16(fs.data[33:35], uint16(fs.sectorSize))
		binary.LittleEndian.PutUint16(fs.data[35:37], uint16((fs.sectorSize-4)/2))
		fs.data[37], fs.data[38], fs.data[39] = 1, 0, 0x5a
	}
	binary.LittleEndian.PutUint16(fs.data[40:42], uint16(fs.bootMap))
	return fs, nil
}

func hex(value uint32) byte { return "0123456789ABCDEF"[value&15] }
func (fs *spartaFS) sector(number int) []byte {
	start := fs.sectorSize * (number - 1)
	return fs.data[start : start+fs.sectorSize]
}
func (fs *spartaFS) bitmapData() []byte {
	start := fs.sectorSize * (fs.bitmap - 1)
	return fs.data[start : start+fs.bitmapCount*fs.sectorSize]
}
func (fs *spartaFS) freeSector(number int) { fs.bitmapData()[number>>3] |= 128 >> (number & 7) }
func (fs *spartaFS) used(number int) bool {
	return fs.bitmapData()[number>>3]&(128>>(number&7)) == 0
}
func (fs *spartaFS) allocate() (int, error) {
	sector := fs.current
	for sector <= fs.sectors && fs.used(sector) {
		sector++
	}
	if sector > fs.sectors {
		return 0, errNoSpace
	}
	fs.bitmapData()[sector>>3] &^= 128 >> (sector & 7)
	fs.current = sector + 1
	return sector, nil
}
func (fs *spartaFS) addData(data []byte) (int, error) {
	first, last := 0, 0
	var previousMap []byte
	remaining := data
	for {
		mapSector, err := fs.allocate()
		if err != nil {
			return 0, err
		}
		if previousMap != nil {
			binary.LittleEndian.PutUint16(previousMap[0:2], uint16(mapSector))
		} else {
			first = mapSector
		}
		currentMap := fs.sector(mapSector)
		binary.LittleEndian.PutUint16(currentMap[2:4], uint16(last))
		for pos := 4; pos < fs.sectorSize && len(remaining) > 0; pos += 2 {
			length := fs.sectorSize
			if len(remaining) < length {
				length = len(remaining)
			}
			dataSector, err := fs.allocate()
			if err != nil {
				return 0, err
			}
			binary.LittleEndian.PutUint16(currentMap[pos:pos+2], uint16(dataSector))
			copy(fs.sector(dataSector), remaining[:length])
			remaining = remaining[length:]
		}
		previousMap, last = currentMap, mapSector
		if len(remaining) == 0 {
			break
		}
	}
	return first, nil
}

// addExtentData keeps EOSFS sectors together when it can, then describes them
// as start/count pairs.
func (fs *spartaFS) addExtentData(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	dataSectors := make([]int, 0, (len(data)+fs.sectorSize-1)/fs.sectorSize)
	for len(data) > 0 {
		sector, err := fs.allocate()
		if err != nil {
			return 0, err
		}
		length := fs.sectorSize
		if len(data) < length {
			length = len(data)
		}
		copy(fs.sector(sector), data[:length])
		data = data[length:]
		dataSectors = append(dataSectors, sector)
	}
	type extent struct{ start, count int }
	extents := make([]extent, 0, len(dataSectors))
	for _, sector := range dataSectors {
		if len(extents) > 0 && extents[len(extents)-1].start+extents[len(extents)-1].count == sector {
			extents[len(extents)-1].count++
		} else {
			extents = append(extents, extent{start: sector, count: 1})
		}
	}
	perList := (fs.sectorSize - 4) / 4
	listCount := (len(extents) + perList - 1) / perList
	lists := make([]int, listCount)
	for i := range lists {
		sector, err := fs.allocate()
		if err != nil {
			return 0, err
		}
		lists[i] = sector
	}
	for i, sector := range lists {
		block := fs.sector(sector)
		if i+1 < len(lists) {
			binary.LittleEndian.PutUint16(block[0:2], uint16(lists[i+1]))
		}
		if i > 0 {
			binary.LittleEndian.PutUint16(block[2:4], uint16(lists[i-1]))
		}
		chunk := extents[i*perList:]
		if len(chunk) > perList {
			chunk = chunk[:perList]
		}
		for position, item := range chunk {
			offset := 4 + position*4
			binary.LittleEndian.PutUint16(block[offset:offset+2], uint16(item.start))
			binary.LittleEndian.PutUint16(block[offset+2:offset+4], uint16(item.count))
		}
	}
	return lists[0], nil
}

func (fs *spartaFS) patchByte(mapSector, position int, value byte) error {
	if fs.eosfs {
		if mapSector < 1 || mapSector > fs.sectors {
			return errors.New("invalid extent list")
		}
		remaining := position / fs.sectorSize
		inSector := position % fs.sectorSize
		for mapSector != 0 {
			list := fs.sector(mapSector)
			for offset := 4; offset+4 <= fs.sectorSize; offset += 4 {
				start := int(binary.LittleEndian.Uint16(list[offset : offset+2]))
				count := int(binary.LittleEndian.Uint16(list[offset+2 : offset+4]))
				if count == 0 {
					return errors.New("position beyond EOSFS extent list")
				}
				if remaining < count {
					if start == 0 {
						return errors.New("cannot patch an EOSFS sparse gap")
					}
					fs.sector(start + remaining)[inSector] = value
					return nil
				}
				remaining -= count
			}
			mapSector = int(binary.LittleEndian.Uint16(list[:2]))
		}
		return errors.New("position beyond EOSFS extent list")
	}
	mapPosition := 4
	for position >= fs.sectorSize {
		position -= fs.sectorSize
		mapPosition += 2
		if mapPosition >= fs.sectorSize {
			mapPosition = 4
			mapSector = int(binary.LittleEndian.Uint16(fs.sector(mapSector)[:2]))
			if mapSector < 1 || mapSector > fs.sectors {
				return errors.New("invalid sector map")
			}
		}
	}
	sector := int(binary.LittleEndian.Uint16(fs.sector(mapSector)[mapPosition : mapPosition+2]))
	if sector < 1 || sector > fs.sectors {
		return errors.New("invalid data sector")
	}
	fs.sector(sector)[position] = value
	return nil
}

func nameHash(name []byte) byte {
	var hash byte
	for _, value := range name {
		hash = (hash<<1 | hash>>7) ^ value
	}
	if hash == 0 {
		return 1
	}
	return hash
}
func (fs *spartaFS) freeSectors() int { return fs.sectors - fs.current + 1 }
func (fs *spartaFS) writeBoot(page int, custom []byte) {
	if fs.sectors < 3 {
		return
	}
	data := make([]byte, 384)
	if len(custom) == 384 {
		copy(data, custom)
	} else {
		boot, reloc := boot128[:], boot128Reloc[:]
		if fs.sectorSize == 256 {
			boot, reloc = boot256[:], boot256Reloc[:]
		}
		copy(data, boot)
		for _, position := range reloc {
			data[position-1] = byte(int(data[position-1]) + page - 16)
		}
	}
	for i := 0; i < 3; i++ {
		copy(fs.data[fs.sectorSize*i:fs.sectorSize*i+128], data[128*i:128*(i+1)])
	}
}

func volumeName(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	var out [8]byte
	for index := range out {
		out[index] = ' '
	}
	for index := 0; index < len(value) && index < len(out); index++ {
		c := value[index]
		if (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
			out[index] = c
		} else {
			out[index] = '_'
		}
	}
	return string(out[:])
}
