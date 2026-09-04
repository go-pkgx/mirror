# mirror

[![pkg.go.dev](https://img.shields.io/badge/pkg.go.dev-mirror-007d9c?logo=go&logoColor=white)](https://pkg.go.dev/github.com/go-pkgx/mirror)
![Go](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go&logoColor=white)
![License](https://img.shields.io/badge/license-BSD--3--Clause-blue)

Sync a **local mirror of pkgx bottles** from an upstream pkgx distribution. It
writes the same directory layout the upstream serves —
`<dest>/<project>/<os>/<arch>/versions.txt` and `v<ver>.tar.{gz,xz}` — so the
result can be served by any static file server (nginx, S3, Caddy…) and used by
[pkgm](https://github.com/go-pkgx/pkgm) / [pkgx](https://github.com/go-pkgx/pkgx)
via `PKGX_DIST=<mirror-url>`.

Part of the pure-Go [go-pkgx](https://github.com/go-pkgx) family; shares the
pkgx bottle protocol with pkgm and pkgx through
[`go-pkgx/bottle`](https://github.com/go-pkgx/bottle). `CGO_ENABLED=0`, one
static binary.

## Install

**Linux / macOS** — one line, naming the release you want:

```sh
curl -fsSL https://go-pkgx.github.io/install.sh | sh -s -- mirror v0.1.3
```

**Windows** (PowerShell) — `irm | iex` passes no arguments, so the version goes
in the environment:

```powershell
$env:PKGX_TOOL='mirror'; $env:MIRROR_VERSION='v0.1.3'; irm https://go-pkgx.github.io/install.ps1 | iex
```

The installer downloads the static binary for your os/arch from that
[release](https://github.com/go-pkgx/mirror/releases), verifies it against the
release `SHA256SUMS`, and drops `mirror` on your `PATH` (`$HOME/.local/bin`, or
`%LOCALAPPDATA%\Programs\go-pkgx` on Windows; `MIRROR_INSTALL` overrides the
directory on Unix). Or `go install github.com/go-pkgx/mirror@latest`.

The version is named on purpose: this line copied today and the same line
copied in six months install the same bytes, and a bad release does not reach
everyone who happens to install that hour. To track releases instead, say so —
`sh -s -- mirror latest`, or `MIRROR_VERSION=latest`.

## Usage

```
mirror <project>[@constraint] ...   mirror the given projects' bottles

  -d, --dest DIR     mirror root (default $PKGX_MIRROR or ./mirror)
      --from URL     upstream dist to mirror from (default https://dist.pkgx.dev)
      --to REF       ALSO push each bottle to an OCI registry
                     (e.g. oci://ghcr.io/you/bottles); additive to --dest
      --os OS        restrict to an OS (repeatable; default: linux, darwin)
      --arch ARCH    restrict to an arch (repeatable; default: x86-64, aarch64)
      --all-versions mirror every version (default: the latest matching one)
      --closure      also mirror each project's full runtime dependency closure
  -n, --dry-run      report what would be fetched without downloading
```

`--to` mirrors into an OCI registry alongside (or instead of) a `--dest` tree;
anonymous push works for public pulls, or supply `OCI_TOKEN` /
`OCI_USERNAME` + `OCI_PASSWORD`.

```console
# mirror git-crypt and everything needed to run it, for linux/aarch64
$ mirror --closure agwa.name/git-crypt --os linux --arch aarch64 --dest ./m
  fetch  agwa.name/git-crypt v0.8.0 linux/aarch64 (78 KiB)
  fetch  openssl.org v1.1.1w linux/aarch64 (3889 KiB)
  …

# serve it and point tools at the mirror
$ (cd ./m && python3 -m http.server 8080) &
# a plain static (HTTP) mirror carries no signatures, so opt out of the
# default-on signature check when consuming it (PKGX_VERIFY=0)
$ PKGX_VERIFY=0 PKGX_DIST=http://localhost:8080 pkgm run agwa.name/git-crypt -- --version
git-crypt 0.8.0
```

It is incremental — bottles already present are skipped — and re-runnable to
keep a mirror up to date. `--closure` pins each dependency to the version the
resolver chose (so a package needing OpenSSL 1.1 gets `libssl.so.1.1`, not the
latest 3.x).

## Where it is proven to work

A mirror is most useful on the machine that has no internet and is not an
x86 laptop, so CI builds nine targets — linux and darwin on amd64 and arm64,
plus linux on riscv64, ppc64le, s390x and loong64 — and then **runs the suite**
on five of them under `qemu-user`, with `-count=1` so a cached PASS from the
host architecture cannot stand in for a run that never happened:

```
test (arm64, qemu)  test (riscv64, qemu)  test (ppc64le, qemu)
test (s390x, qemu)  test (loong64, qemu)
```

Cross-compiling proves the code builds for an architecture and says nothing
about whether it works there. s390x is in that list because it is big-endian
and nothing else here is, and mirroring bottles means reading tar, xz and OCI
manifests this code did not write.

BSD-3-Clause.
