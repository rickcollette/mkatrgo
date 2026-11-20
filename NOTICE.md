# license + where this came from

the SpartaDOS/BW-DOS filesystem code and the original bootloader data are a Go
port of [`dmsc/mkatr`](https://github.com/dmsc/mkatr), copyright (C) 2016–2026
Daniel Serpell.

that code is GNU GPL version 2 only (the full license is in `LICENSE`). this is
also why `atr` stays a separate executable ... the MIT-licensed exBASIC Studio
process does not link the GPL implementation.

the EOSFS work follows ExtendedOS's public-domain/CC0 “EOSFS: the EOS native
file system, Version 1” specification. the EOSFS-only extent/index code is CC0
where it can be separated, but the combined executable is still GPL-2.0-only
(it also contains the mkatr-derived SpartaDOS code).

`bootloader.go` embeds the 384-byte EOSFS loader built from
`../ExtendedOS/eos/carcmd/EOSFSBOOT.asm` and distributed as
`../ExtendedOS/DISTRIBUTE/EOSFSBOOT.bin`.

sha-256 (so we know exactly which one):

```text
1d4876d7033bf7e411d68ad2de262497ec9ebf81a6c33d74d708bbf4c1c73a1b
```
