# atr

this is the atr tool i kept wanting ... one binary for making images, looking
inside them, moving files in and out, changing things, and making them boot.

the library is usable on its own too (the command is just a thin layer over
it).

## build it

```sh
make build
```

that makes all four release builds:

```text
dist/atr-linux-amd64
dist/atr-windows-amd64.exe
dist/atr-darwin-amd64
dist/atr-darwin-arm64
```

intel means amd64/x86-64 here. the shorter build targets are:

```sh
make linux       # linux (intel/amd64)
make windows     # windows (intel/amd64)
make mac-intel   # macos (intel/amd64)
make mac-arm64   # macos (apple silicon)
```

some other useful ones ...

```sh
make native      # ./atr for this machine
make check       # formatting check + vet + tests
make race        # tests with the race detector
make install     # go install
make clean       # remove local build/test/coverage/profile artifacts
make help        # the short list
```

`make clean` stays inside this checkout (it does not trash Go's shared global
build/module caches ... those belong to every Go project on the machine).

release builds are static (`CGO_ENABLED=0`), trimmed, and version stamped. if
there is no git tag/hash available the version is `dev` (or set it yourself):

```sh
make build VERSION=1.0.0
```

## use it

```text
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
```

there are a few aliases (because i will absolutely forget which name i used):

```text
ls    -> list
cpin  -> put
cpout -> get
mkatr -> create
```

flags go before the positional stuff ... that is how Go's flag parser works.
`atr help` has the complete short version.

## normal examples

make an SDFS disk that boots a normal XEX:

```sh
atr create --profile sparta --boot GAME.XEX game.atr GAME.XEX README.TXT
```

copy things in/out (editing keeps the image geometry and its boot sectors):

```sh
atr put --to TOOLS disk.atr host-tools/
atr get disk.atr TOOLS/EDIT.COM edit.com
atr extract disk.atr extracted/
```

make an EOSFS boot disk. this uses the new extent-aware ExtendedOS loader
(already embedded ... no sibling checkout needed at runtime):

```sh
atr create --profile eos --boot EOS.SYS eos.atr EOS.SYS CONFIG.SYS
```

make an Atari DOS 2 disk. the boot sectors come from a DOS master ATR; the OS
files are yours (i am not bundling DOS.SYS/DUP.SYS in here):

```sh
atr create --profile dos2 --loader DOS20.ATR --boot DOS.SYS \
  dos2.atr DOS.SYS DUP.SYS
```

BW-DOS uses SDFS. the generic loader works, or give it `FORMAT.COM` and keep
the native three-sector loader:

```sh
atr create --profile bwdos --loader FORMAT.COM --boot XBW15.DOS \
  bwdos.atr XBW15.DOS STARTUP.BAT
```

## what works

| format | list/extract | create/edit | boot |
|---|---:|---:|---:|
| SpartaDOS SDFS 2.0/2.1 | yes | yes | generic XEX or a supplied native loader |
| BW-DOS SDFS | yes | yes | generic `.DOS` or native `FORMAT.COM` loader |
| EOSFS v1 | yes | yes | embedded ExtendedOS `EOSFSBOOT.bin` |
| Atari DOS 2.0S (720 x 128) | yes | yes | boot sectors from a DOS master ATR |
| Atari DOS 2.0D (720 x 256) | yes | yes | boot sectors from a DOS master ATR |

Atari DOS 2 is flat (64 entries, no directories). i reject directories instead
of quietly making something that is really MyDOS ... less surprising that way.

DOS 2.5 enhanced-density and MyDOS images can be inspected when the usual
directory/sector-chain layout is recognized. they are not rewritten as DOS
2.0 images.

the embedded EOS loader is the 384-byte build from
`../ExtendedOS/eos/carcmd/EOSFSBOOT.asm`. `--loader` can replace it with raw
boot sectors, a loader inside an Atari executable, or the boot sectors from an
ATR.

## layout (for me later ...)

```text
cmd/atr/          tiny executable entry point
internal/cli/     args, help, and output
*.go              atr/filesystem library
*_test.go         format + round-trip tests
```

## not here yet

the older C `lsatr` also knows DOS 1, LiteDOS, Bibo-DOS, BAS2BOOT, Howfen DOS,
and K-file boot images. those are not drivers here yet ... unsupported media
gets an error instead of being guessed at (especially for writes).
