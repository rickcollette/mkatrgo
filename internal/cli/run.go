// SPDX-License-Identifier: GPL-2.0-only

package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	atr "github.com/megalith/mkatrgo"
)

// Version starts as dev ... the Makefile stamps release builds over it.
var Version = "dev"

const usage = `atr ... atr image tools (create, list, copy, boot, etc.)

usage:
  atr info IMAGE
  atr list [--json] IMAGE [PATH]
  atr create [options] IMAGE [HOST-PATH...]
  atr put [--to ATR-DIR] IMAGE HOST-PATH...
  atr get IMAGE ATR-PATH [HOST-PATH]
  atr extract IMAGE HOST-DIR
  atr mkdir IMAGE ATR-DIR...
  atr rm IMAGE ATR-PATH...
  atr mv IMAGE OLD-PATH NEW-PATH
  atr attr IMAGE MODE ATR-PATH...
  atr chvol IMAGE NAME
  atr mkboot [--profile sparta|bwdos|dos2|eos] [--loader FILE] IMAGE ATR-FILE
  atr verify IMAGE
  atr version

aliases (because i will forget): ls=list, cpin=put, cpout=get, mkatr=create

create options:
  --filesystem sparta|dos2|eos  filesystem to create (default sparta)
  --profile NAME                sparta, bwdos, dos2, or eos
  --boot ATR-FILE               file in the image to boot
  --loader FILE                 raw loader, FORMAT.COM, or a master ATR
  --volume NAME                 volume name (8 characters max)
  --sector-size 128|256         explicit sector size (use with --sectors)
  --sectors COUNT               explicit sector count (use with --sector-size)
  --size BYTES                  minimum payload size
  --exact                       smallest geometry that fits (no rounding up)
  --boot-page PAGE              generic loader page (decimal or 0x-prefixed)
`

// Run runs one command and returns the exit code (0 is good ... anything else is not).
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, usage)
		return 0
	}
	command := map[string]string{"ls": "list", "cpin": "put", "cpout": "get", "mkatr": "create"}[args[0]]
	if command == "" {
		command = args[0]
	}
	var err error
	switch command {
	case "info":
		err = runInfo(args[1:], stdout)
	case "list":
		err = runList(args[1:], stdout, stderr)
	case "create":
		err = runCreate(args[1:], stdout, stderr)
	case "put":
		err = runPut(args[1:], stdout, stderr)
	case "get":
		err = runGet(args[1:])
	case "extract":
		err = runExtract(args[1:])
	case "mkdir":
		err = runMkdir(args[1:])
	case "rm":
		err = runRemove(args[1:])
	case "mv":
		err = runMove(args[1:])
	case "attr":
		err = runAttributes(args[1:])
	case "chvol":
		err = runVolume(args[1:])
	case "mkboot":
		err = runMkboot(args[1:], stdout, stderr)
	case "verify":
		err = runVerify(args[1:], stdout)
	case "version":
		if err = exactArgs(args[1:], 0, "atr version"); err == nil {
			fmt.Fprintln(stdout, Version)
		}
	default:
		err = fmt.Errorf("unknown command %q ... try `atr help`", args[0])
	}
	if err != nil {
		fmt.Fprintf(stderr, "atr: %v\n", err)
		return 1
	}
	return 0
}

func flags(name string, stderr io.Writer) *flag.FlagSet {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(stderr)
	return set
}

func exactArgs(args []string, count int, syntax string) error {
	if len(args) != count {
		return fmt.Errorf("usage: %s", syntax)
	}
	return nil
}

func runInfo(args []string, output io.Writer) error {
	if err := exactArgs(args, 1, "atr info IMAGE"); err != nil {
		return err
	}
	image, err := atr.OpenImage(args[0])
	if err != nil {
		return err
	}
	fmt.Fprintf(output, "%s: %s revision $%02X, volume %q, %d sectors x %d bytes, %d bytes free\n",
		args[0], image.Filesystem, image.Revision, image.Volume, image.Sectors, image.SectorSize, image.FreeBytes)
	return nil
}

func runList(args []string, output, stderr io.Writer) error {
	set := flags("list", stderr)
	jsonOutput := set.Bool("json", false, "write JSON")
	if err := set.Parse(args); err != nil {
		return err
	}
	if len(set.Args()) < 1 || len(set.Args()) > 2 {
		return errors.New("usage: atr list [--json] IMAGE [PATH]")
	}
	image, err := atr.OpenImage(set.Args()[0])
	if err != nil {
		return err
	}
	prefix := ""
	if len(set.Args()) == 2 {
		prefix = strings.Trim(strings.ReplaceAll(set.Args()[1], ">", "/"), "/")
	}
	entries := make([]atr.ImageEntry, 0, len(image.Entries))
	for _, entry := range image.Entries {
		if prefix == "" || strings.EqualFold(entry.Path, prefix) || strings.HasPrefix(strings.ToUpper(entry.Path), strings.ToUpper(prefix)+"/") {
			entries = append(entries, entry)
		}
	}
	if *jsonOutput {
		return json.NewEncoder(output).Encode(struct {
			Filesystem string           `json:"filesystem"`
			Revision   int              `json:"revision"`
			Volume     string           `json:"volume"`
			Entries    []atr.ImageEntry `json:"entries"`
		}{image.Filesystem, image.Revision, image.Volume, entries})
	}
	for _, entry := range entries {
		kind := " "
		if entry.Directory {
			kind = "d"
		}
		boot := " "
		if entry.Boot {
			boot = "*"
		}
		modified := "-                  "
		if !entry.Modified.IsZero() {
			modified = entry.Modified.Format("2006-01-02 15:04:05")
		}
		fmt.Fprintf(output, "%s%s %8d %s %s %s\n", kind, boot, entry.Size, attributeText(entry.Attributes), modified, entry.Path)
	}
	return nil
}

func attributeText(value atr.Attributes) string {
	var text [3]byte
	for index := range text {
		text[index] = '-'
	}
	if value&atr.AttributeProtected != 0 {
		text[0] = 'p'
	}
	if value&atr.AttributeHidden != 0 {
		text[1] = 'h'
	}
	if value&atr.AttributeArchived != 0 {
		text[2] = 'a'
	}
	return string(text[:])
}

type createOptions struct {
	filesystem, profile, boot, loader, volume string
	sectorSize, sectors, minimum, bootPage    int
	exact                                     bool
}

func runCreate(args []string, output, stderr io.Writer) error {
	set := flags("create", stderr)
	var options createOptions
	set.StringVar(&options.filesystem, "filesystem", "sparta", "filesystem")
	set.StringVar(&options.profile, "profile", "", "boot profile")
	set.StringVar(&options.boot, "boot", "", "ATR boot file")
	set.StringVar(&options.loader, "loader", "", "boot loader")
	set.StringVar(&options.volume, "volume", "", "volume name")
	set.IntVar(&options.sectorSize, "sector-size", 0, "sector size")
	set.IntVar(&options.sectors, "sectors", 0, "sector count")
	set.IntVar(&options.minimum, "size", 0, "minimum payload size")
	set.BoolVar(&options.exact, "exact", false, "exact geometry")
	bootPage := set.String("boot-page", "7", "generic loader page")
	if err := set.Parse(args); err != nil {
		return err
	}
	if len(set.Args()) < 1 {
		return errors.New("usage: atr create [options] IMAGE [HOST-PATH...]")
	}
	page, err := strconv.ParseInt(*bootPage, 0, 32)
	if err != nil {
		return fmt.Errorf("boot page does not make sense: %w", err)
	}
	options.bootPage = int(page)
	fs, revision, bootCode, err := resolveProfile(options.filesystem, options.profile, options.loader, options.boot != "")
	if err != nil {
		return err
	}
	builder := atr.NewBuilder()
	result, err := builder.Create(set.Args()[0], atr.Options{Filesystem: fs, Revision: revision, Volume: options.volume, ExactSize: options.exact, MinimumSize: options.minimum, BootPage: options.bootPage, SectorSize: options.sectorSize, Sectors: options.sectors, BootSectors: bootCode})
	if err != nil {
		return err
	}
	if len(set.Args()) > 1 || options.boot != "" {
		image, openErr := atr.OpenImage(set.Args()[0])
		if openErr != nil {
			return openErr
		}
		if len(set.Args()) > 1 {
			if err := image.Put(set.Args()[1:], ""); err != nil {
				return err
			}
		}
		if options.boot != "" {
			if err := image.SetBoot(options.boot); err != nil {
				return err
			}
		}
		if err := image.Save(set.Args()[0]); err != nil {
			return err
		}
	}
	fmt.Fprintf(output, "created %s: %d sectors x %d bytes (%d-byte ATR)\n", set.Args()[0], result.Sectors, result.SectorSize, result.Bytes)
	return nil
}

func resolveProfile(filesystem, profile, loader string, boot bool) (string, int, []byte, error) {
	filesystem = strings.ToLower(filesystem)
	profile = strings.ToLower(profile)
	if profile == "" {
		if filesystem == "eos" || filesystem == "eosfs" {
			profile = "eos"
		} else if filesystem == "dos2" || filesystem == "atari-dos2" || filesystem == "ataridos" {
			profile = "dos2"
		} else {
			profile = "sparta"
		}
	}
	var revision int
	switch profile {
	case "sparta", "spartados":
		profile = "sparta"
		filesystem, revision = "sparta", 0x20
	case "bwdos", "bw-dos":
		profile = "bwdos"
		filesystem, revision = "sparta", 0x21
	case "eos", "eosfs":
		profile = "eos"
		filesystem, revision = "eos", 0x45
	case "dos2", "atari-dos2":
		profile = "dos2"
		filesystem, revision = "dos2", 2
	default:
		return "", 0, nil, fmt.Errorf("unknown boot profile %q (sparta, bwdos, dos2, or eos)", profile)
	}
	var code []byte
	var err error
	if loader != "" {
		code, err = atr.LoadBootSectors(loader)
	} else if profile == "eos" && boot {
		code = atr.EOSBootSectors()
	} else if profile == "dos2" && boot {
		return "", 0, nil, errors.New("Atari DOS 2 boot disks need --loader (raw boot sectors or a DOS master ATR)")
	}
	return filesystem, revision, code, err
}

func runPut(args []string, output, stderr io.Writer) error {
	set := flags("put", stderr)
	destination := set.String("to", "", "ATR destination directory")
	if err := set.Parse(args); err != nil {
		return err
	}
	if len(set.Args()) < 2 {
		return errors.New("usage: atr put [--to ATR-DIR] IMAGE HOST-PATH...")
	}
	image, err := atr.OpenImage(set.Args()[0])
	if err != nil {
		return err
	}
	if err := image.Put(set.Args()[1:], *destination); err != nil {
		return err
	}
	if err := image.Save(set.Args()[0]); err != nil {
		return err
	}
	fmt.Fprintf(output, "updated %s\n", set.Args()[0])
	return nil
}

func runGet(args []string) error {
	if len(args) < 2 || len(args) > 3 {
		return errors.New("usage: atr get IMAGE ATR-PATH [HOST-PATH]")
	}
	image, err := atr.OpenImage(args[0])
	if err != nil {
		return err
	}
	output := filepath.Base(strings.TrimRight(strings.ReplaceAll(args[1], ">", "/"), "/"))
	if len(args) == 3 {
		output = args[2]
	}
	return image.Extract(strings.ReplaceAll(args[1], ">", "/"), output)
}

func runExtract(args []string) error {
	if err := exactArgs(args, 2, "atr extract IMAGE HOST-DIR"); err != nil {
		return err
	}
	image, err := atr.OpenImage(args[0])
	if err != nil {
		return err
	}
	return image.ExtractAll(args[1])
}

func runMkdir(args []string) error {
	if len(args) < 2 {
		return errors.New("usage: atr mkdir IMAGE ATR-DIR...")
	}
	image, err := atr.OpenImage(args[0])
	if err != nil {
		return err
	}
	for _, path := range args[1:] {
		if err := image.Mkdir(path); err != nil {
			return err
		}
	}
	return image.Save(args[0])
}

func runRemove(args []string) error {
	if len(args) < 2 {
		return errors.New("usage: atr rm IMAGE ATR-PATH...")
	}
	image, err := atr.OpenImage(args[0])
	if err != nil {
		return err
	}
	for _, path := range args[1:] {
		if err := image.Remove(path); err != nil {
			return err
		}
	}
	return image.Save(args[0])
}

func runMove(args []string) error {
	if err := exactArgs(args, 3, "atr mv IMAGE OLD-PATH NEW-PATH"); err != nil {
		return err
	}
	image, err := atr.OpenImage(args[0])
	if err != nil {
		return err
	}
	if err := image.Rename(args[1], args[2]); err != nil {
		return err
	}
	return image.Save(args[0])
}

func runAttributes(args []string) error {
	if len(args) < 3 {
		return errors.New("usage: atr attr IMAGE MODE ATR-PATH...")
	}
	set, clear, err := parseAttributeMode(args[1])
	if err != nil {
		return err
	}
	image, err := atr.OpenImage(args[0])
	if err != nil {
		return err
	}
	for _, path := range args[2:] {
		if err := image.ChangeAttributes(path, set, clear); err != nil {
			return err
		}
	}
	return image.Save(args[0])
}

func parseAttributeMode(mode string) (atr.Attributes, atr.Attributes, error) {
	var set, clear atr.Attributes
	operation := byte(0)
	for index := 0; index < len(mode); index++ {
		switch mode[index] {
		case '+', '-':
			operation = mode[index]
		case 'p', 'P', 'h', 'H', 'a', 'A':
			if operation == 0 {
				return 0, 0, errors.New("attribute mode must begin with + or -")
			}
			value := atr.AttributeProtected
			if mode[index] == 'h' || mode[index] == 'H' {
				value = atr.AttributeHidden
			} else if mode[index] == 'a' || mode[index] == 'A' {
				value = atr.AttributeArchived
			}
			if operation == '+' {
				set |= value
				clear &^= value
			} else {
				clear |= value
				set &^= value
			}
		default:
			return 0, 0, fmt.Errorf("unknown attribute %q", mode[index])
		}
	}
	if set|clear == 0 {
		return 0, 0, errors.New("attribute mode contains no attributes")
	}
	return set, clear, nil
}

func runVolume(args []string) error {
	if err := exactArgs(args, 2, "atr chvol IMAGE NAME"); err != nil {
		return err
	}
	image, err := atr.OpenImage(args[0])
	if err != nil {
		return err
	}
	if image.Filesystem == "dos2" {
		return errors.New("Atari DOS 2 has no volume-name field ... there is nothing to change")
	}
	image.Volume = args[1]
	return image.Save(args[0])
}

func runMkboot(args []string, output, stderr io.Writer) error {
	set := flags("mkboot", stderr)
	profile := set.String("profile", "", "boot profile")
	loader := set.String("loader", "", "boot loader")
	if err := set.Parse(args); err != nil {
		return err
	}
	if len(set.Args()) != 2 {
		return errors.New("usage: atr mkboot [--profile sparta|bwdos|dos2|eos] [--loader FILE] IMAGE ATR-FILE")
	}
	image, err := atr.OpenImage(set.Args()[0])
	if err != nil {
		return err
	}
	selected := *profile
	if selected == "" {
		selected = image.Filesystem
	}
	fs, revision, code, err := resolveProfile(image.Filesystem, selected, *loader, true)
	if err != nil {
		return err
	}
	if fs != image.Filesystem {
		return fmt.Errorf("profile %s does not match this image (%s filesystem)", selected, image.Filesystem)
	}
	image.Revision = revision
	if code != nil {
		if err := image.SetBootSectors(code); err != nil {
			return err
		}
	}
	if err := image.SetBoot(set.Args()[1]); err != nil {
		return err
	}
	if err := image.Save(set.Args()[0]); err != nil {
		return err
	}
	fmt.Fprintf(output, "%s now boots %s (%s profile)\n", set.Args()[0], set.Args()[1], selected)
	return nil
}

func runVerify(args []string, output io.Writer) error {
	if err := exactArgs(args, 1, "atr verify IMAGE"); err != nil {
		return err
	}
	image, err := atr.OpenImage(args[0])
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(image.Entries))
	for _, entry := range image.Entries {
		paths = append(paths, strings.ToUpper(entry.Path))
	}
	sort.Strings(paths)
	for index := 1; index < len(paths); index++ {
		if paths[index] == paths[index-1] {
			return fmt.Errorf("duplicate image path %q", paths[index])
		}
	}
	fmt.Fprintf(output, "%s: ok (%s, %d entries)\n", args[0], image.Filesystem, len(image.Entries))
	return nil
}
