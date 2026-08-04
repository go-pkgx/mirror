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

## Usage

```
mirror <project>[@constraint] ...   mirror the given projects' bottles

  -d, --dest DIR     mirror root (default $PKGX_MIRROR or ./mirror)
      --from URL     upstream dist to mirror from (default https://dist.pkgx.dev)
      --os OS        restrict to an OS (repeatable; default: linux, darwin)
      --arch ARCH    restrict to an arch (repeatable; default: x86-64, aarch64)
      --all-versions mirror every version (default: the latest matching one)
      --closure      also mirror each project's full runtime closure + the
                     implicit FROM-scratch system libs (glibc, libstdc++)
  -n, --dry-run      report what would be fetched without downloading
```

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
latest 3.x). BSD-3-Clause.
