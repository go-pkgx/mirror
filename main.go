// Command mirror synchronises a local mirror of pkgx bottles from an upstream
// pkgx distribution (dist.pkgx.dev by default). It writes the same directory
// layout the upstream serves — <dest>/<project>/<os>/<arch>/versions.txt and
// v<ver>.tar.{gz,xz} — so the result can be served by any static file server
// (nginx, S3, …) and used by pkgm/pkgx via `PKGX_DIST=<mirror-url>`.
//
// It shares the pkgx bottle protocol (URLs, version parsing, tarball fetch)
// with pkgm and pkgx through the github.com/go-pkgx/bottle package, so there is
// one source of truth. Pure Go, CGO_ENABLED=0.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-pkgx/bottle"
)

const version = "0.1.0"

const usage = `mirror ` + version + ` — sync a local mirror of pkgx bottles

usage:
  mirror <project>[@constraint] ...   mirror the given projects' bottles

flags:
  -d, --dest DIR     mirror root (default $PKGX_MIRROR or ./mirror)
      --from URL     upstream dist to mirror from (default https://dist.pkgx.dev)
      --os OS        restrict to an OS (repeatable; default: linux, darwin)
      --arch ARCH    restrict to an arch (repeatable; default: x86-64, aarch64)
      --all-versions mirror every version (default: the latest matching one)
      --closure      also mirror each project's full runtime dependency closure
  -n, --dry-run      report what would be fetched without downloading
  -h, --help         show this help
  -v, --version      show version

Serve <dest> statically and point tools at it:  PKGX_DIST=https://my.mirror pkgx …
`

type opts struct {
	dest        string
	from        string
	oses        []string
	arches      []string
	allVersions bool
	closure     bool
	dryRun      bool
}

func main() { os.Exit(run(os.Args[1:])) }

func run(argv []string) int {
	pos, o, help, showVer, err := parseArgs(argv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mirror: "+err.Error())
		return 2
	}
	if help {
		fmt.Print(usage)
		return 0
	}
	if showVer {
		fmt.Println("mirror " + version)
		return 0
	}
	if len(pos) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	// The upstream to pull from is explicit (NOT $PKGX_DIST, which is for
	// consumers reading a mirror) — set it on the shared backend.
	bottle.DistBase = strings.TrimRight(o.from, "/")
	if err := mirror(pos, o); err != nil {
		fmt.Fprintln(os.Stderr, "mirror: "+err.Error())
		return 1
	}
	return 0
}

// parseArgs splits flags from positional package specs.
func parseArgs(argv []string) (pos []string, o opts, help, showVer bool, err error) {
	o = opts{dest: defaultDest(), from: "https://dist.pkgx.dev"}
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		next := func() (string, bool) {
			if i+1 < len(argv) {
				i++
				return argv[i], true
			}
			return "", false
		}
		switch {
		case a == "-h" || a == "--help":
			help = true
		case a == "-v" || a == "--version":
			showVer = true
		case a == "-n" || a == "--dry-run":
			o.dryRun = true
		case a == "--all-versions":
			o.allVersions = true
		case a == "--closure":
			o.closure = true
		case a == "-d" || a == "--dest":
			if v, ok := next(); ok {
				o.dest = v
			}
		case strings.HasPrefix(a, "--dest="):
			o.dest = strings.TrimPrefix(a, "--dest=")
		case a == "--from":
			if v, ok := next(); ok {
				o.from = v
			}
		case strings.HasPrefix(a, "--from="):
			o.from = strings.TrimPrefix(a, "--from=")
		case a == "--os":
			if v, ok := next(); ok {
				o.oses = append(o.oses, v)
			}
		case a == "--arch":
			if v, ok := next(); ok {
				o.arches = append(o.arches, v)
			}
		case strings.HasPrefix(a, "-"):
			return nil, o, false, false, fmt.Errorf("unknown flag %q", a)
		default:
			pos = append(pos, a)
		}
	}
	if len(o.oses) == 0 {
		o.oses = []string{"linux", "darwin"}
	}
	if len(o.arches) == 0 {
		o.arches = []string{"x86-64", "aarch64"}
	}
	return pos, o, help, showVer, nil
}

func defaultDest() string {
	if d := os.Getenv("PKGX_MIRROR"); d != "" {
		return d
	}
	return "mirror"
}

// mirror walks every project × os × arch, selects the versions to copy, and
// downloads any that are not already present in the mirror tree.
func mirror(specs []string, o opts) error {
	if o.closure {
		expanded, err := expandClosure(specs)
		if err != nil {
			return err
		}
		specs = expanded
	}
	var fetched, skipped int
	for _, spec := range specs {
		project, constraint := parseSpec(spec)
		for _, osn := range o.oses {
			for _, arch := range o.arches {
				vs, err := bottle.VersionsFor(project, osn, arch)
				if err != nil {
					// no bottles for this os/arch (common) — not fatal.
					fmt.Printf("  skip   %s %s/%s (no versions)\n", project, osn, arch)
					continue
				}
				for _, v := range selectVersions(vs, constraint, o.allVersions) {
					n, s, err := mirrorOne(project, v.Raw, osn, arch, o)
					if err != nil {
						return fmt.Errorf("%s v%s %s/%s: %w", project, v.Raw, osn, arch, err)
					}
					fetched += n
					skipped += s
				}
				if !o.dryRun {
					if err := writeVersions(o.dest, project, osn, arch, selectVersions(vs, constraint, o.allVersions)); err != nil {
						return err
					}
				}
			}
		}
	}
	fmt.Printf("done: %d fetched, %d already present → %s\n", fetched, skipped, o.dest)
	return nil
}

// mirrorOne copies a single bottle if it is not already mirrored. It returns
// (fetched, skipped) counts (each 0 or 1).
func mirrorOne(project, ver, osn, arch string, o opts) (fetched, skipped int, err error) {
	dir := filepath.Join(o.dest, filepath.FromSlash(project), osn, arch)
	// already mirrored (either compression) → skip.
	for _, ext := range []string{".tar.gz", ".tar.xz"} {
		if _, e := os.Stat(filepath.Join(dir, "v"+ver+ext)); e == nil {
			return 0, 1, nil
		}
	}
	if o.dryRun {
		fmt.Printf("  fetch  %s v%s %s/%s (dry-run)\n", project, ver, osn, arch)
		return 1, 0, nil
	}
	data, ext, err := bottle.DownloadBottle(project, ver, osn, arch)
	if err != nil {
		return 0, 0, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, 0, err
	}
	if err := os.WriteFile(filepath.Join(dir, "v"+ver+ext), data, 0o644); err != nil {
		return 0, 0, err
	}
	fmt.Printf("  fetch  %s v%s %s/%s (%d KiB)\n", project, ver, osn, arch, len(data)/1024)
	return 1, 0, nil
}

// writeVersions writes the mirror's versions.txt for a project/os/arch, listing
// the versions present in the mirror (one per line, ascending).
func writeVersions(dest, project, osn, arch string, vs []bottle.Ver) error {
	dir := filepath.Join(dest, filepath.FromSlash(project), osn, arch)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var b strings.Builder
	for _, v := range vs {
		b.WriteString(v.Raw)
		b.WriteByte('\n')
	}
	return os.WriteFile(filepath.Join(dir, "versions.txt"), []byte(b.String()), 0o644)
}

// selectVersions returns the versions to mirror: all that satisfy the
// constraint when allVersions is set, else just the highest matching one.
func selectVersions(vs []bottle.Ver, constraint string, allVersions bool) []bottle.Ver {
	var match []bottle.Ver
	for _, v := range vs { // vs is ascending
		if v.Satisfies(constraint) {
			match = append(match, v)
		}
	}
	if allVersions || len(match) == 0 {
		return match
	}
	return match[len(match)-1:] // the highest
}

// implicitRoots are the system libraries pkgm/pkgx complete from ELF NEEDED on a
// FROM-scratch image (they are not in any package's declared graph): the glibc
// loader+libc that every dynamic ELF needs, and the gcc runtime (libgcc_s /
// libstdc++ / libatomic) that C/C++ bottles link. --closure pulls these (and
// their own deps) so the mirror is complete for a scratch consumer.
var implicitRoots = []string{"gnu.org/glibc", "gnu.org/gcc/libstdcxx"}

// expandClosure resolves each root spec's runtime dependency closure plus the
// implicit FROM-scratch system libraries, and returns the full deduplicated
// list to mirror, each pinned to the version the resolver chose (e.g. git-crypt
// pulls openssl ^1.1, so the mirror gets that 1.1.x — not the latest openssl).
func expandClosure(specs []string) ([]string, error) {
	roots := append(append([]string{}, specs...), implicitRoots...)
	seen := map[string]bool{}
	var out []string
	add := func(project, spec string) {
		if !seen[project] {
			seen[project] = true
			out = append(out, spec)
		}
	}
	for _, spec := range roots {
		project, constraint := parseSpec(spec)
		closure, err := bottle.ResolveClosure(map[string]string{project: constraint})
		if err != nil {
			// The closure resolves against the HOST arch; a root with no bottle
			// there (e.g. gnu.org/glibc has no darwin build) can't be walked —
			// mirror the root itself at latest for the requested arch(es).
			add(project, project)
			continue
		}
		for _, r := range closure {
			add(r.Project, r.Project+"@="+r.Version.Raw)
		}
	}
	return out, nil
}

// parseSpec splits "project[@constraint]" (default constraint "*").
func parseSpec(s string) (project, constraint string) {
	if i := strings.LastIndex(s, "@"); i > 0 {
		return s[:i], s[i+1:]
	}
	return s, "*"
}
