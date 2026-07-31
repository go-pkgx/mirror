package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-pkgx/bottle"
)

func TestParseArgs(t *testing.T) {
	pos, o, help, ver, err := parseArgs([]string{"--os", "linux", "--arch", "aarch64", "-d", "/m", "--all-versions", "-n", "gnu.org/wget@^1"})
	if err != nil {
		t.Fatal(err)
	}
	if help || ver {
		t.Fatal("no help/version expected")
	}
	if len(pos) != 1 || pos[0] != "gnu.org/wget@^1" {
		t.Errorf("pos=%v", pos)
	}
	if o.dest != "/m" || !o.allVersions || !o.dryRun {
		t.Errorf("opts=%+v", o)
	}
	if fmt.Sprint(o.oses) != "[linux]" || fmt.Sprint(o.arches) != "[aarch64]" {
		t.Errorf("os/arch = %v %v", o.oses, o.arches)
	}
	// defaults
	_, d, _, _, _ := parseArgs([]string{"x"})
	if fmt.Sprint(d.oses) != "[linux darwin]" || fmt.Sprint(d.arches) != "[x86-64 aarch64]" {
		t.Errorf("defaults = %v %v", d.oses, d.arches)
	}
	// unknown flag
	if _, _, _, _, err := parseArgs([]string{"--bogus"}); err == nil {
		t.Error("expected error on unknown flag")
	}
}

func TestParseSpecAndSelect(t *testing.T) {
	if p, c := parseSpec("gnu.org/wget"); p != "gnu.org/wget" || c != "*" {
		t.Errorf("parseSpec bare = %q %q", p, c)
	}
	if p, c := parseSpec("node@^22"); p != "node" || c != "^22" {
		t.Errorf("parseSpec = %q %q", p, c)
	}
	vs := []bottle.Ver{bottle.ParseVer("1.0.0"), bottle.ParseVer("1.5.0"), bottle.ParseVer("2.0.0")}
	// latest matching
	if got := selectVersions(vs, "^1", false); len(got) != 1 || got[0].Raw != "1.5.0" {
		t.Errorf("latest ^1 = %v", got)
	}
	// all matching
	if got := selectVersions(vs, "^1", true); len(got) != 2 {
		t.Errorf("all ^1 = %v", got)
	}
	// no match
	if got := selectVersions(vs, "^9", false); len(got) != 0 {
		t.Errorf("^9 = %v", got)
	}
}

// fakeDist serves the pkgx dist layout (versions.txt + v<ver>.tar.gz) AND a
// minimal pantry (package.yml with no dependencies) so ResolveClosure works.
func fakeDist(t *testing.T) (*httptest.Server, func()) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/package.yml"):
			fmt.Fprint(w, "provides:\n  - bin/x\n") // no deps -> closure = {self}
		case strings.HasSuffix(p, "/versions.txt"):
			fmt.Fprint(w, "1.0.0\n2.0.0\n")
		case strings.HasSuffix(p, ".tar.gz"):
			w.Write([]byte("bottle-bytes-" + filepath.Base(p)))
		default:
			http.NotFound(w, r) // .tar.xz etc.
		}
	})
	srv := httptest.NewServer(mux)
	sd, sp := bottle.DistBase, bottle.PantryBase
	bottle.DistBase, bottle.PantryBase = srv.URL, srv.URL
	return srv, func() { bottle.DistBase, bottle.PantryBase = sd, sp; srv.Close() }
}

func TestExpandClosure(t *testing.T) {
	_, cleanup := fakeDist(t)
	defer cleanup()
	out, err := expandClosure([]string{"acme.org/tool"})
	if err != nil {
		t.Fatal(err)
	}
	// the root plus the implicit FROM-scratch roots, each pinned to a version.
	got := map[string]bool{}
	for _, s := range out {
		p, _ := parseSpec(s)
		got[p] = true
	}
	for _, want := range []string{"acme.org/tool", "gnu.org/glibc", "gnu.org/gcc/libstdcxx"} {
		if !got[want] {
			t.Errorf("expandClosure missing %s (got %v)", want, out)
		}
	}
	// pinned to an exact version ("@=").
	if !strings.Contains(strings.Join(out, " "), "acme.org/tool@=2.0.0") {
		t.Errorf("root not pinned: %v", out)
	}
}

func TestMirrorClosure(t *testing.T) {
	_, cleanup := fakeDist(t)
	defer cleanup()
	dest := t.TempDir()
	o := opts{dest: dest, from: bottle.DistBase, oses: []string{"linux"}, arches: []string{"aarch64"}, closure: true}
	if err := mirror([]string{"acme.org/tool"}, o); err != nil {
		t.Fatal(err)
	}
	// the closure (root + implicit roots) is mirrored.
	for _, proj := range []string{"acme.org/tool", "gnu.org/glibc", "gnu.org/gcc/libstdcxx"} {
		f := filepath.Join(dest, filepath.FromSlash(proj), "linux", "aarch64", "v2.0.0.tar.gz")
		if _, err := os.Stat(f); err != nil {
			t.Errorf("closure did not mirror %s: %v", proj, err)
		}
	}
}

func TestMirrorEndToEnd(t *testing.T) {
	_, cleanup := fakeDist(t)
	defer cleanup()
	dest := t.TempDir()
	o := opts{dest: dest, from: bottle.DistBase, oses: []string{"linux"}, arches: []string{"x86-64"}, allVersions: true}

	if err := mirror([]string{"acme.org/tool"}, o); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dest, "acme.org", "tool", "linux", "x86-64")
	for _, f := range []string{"versions.txt", "v1.0.0.tar.gz", "v2.0.0.tar.gz"} {
		if _, err := os.Stat(filepath.Join(base, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
	vt, _ := os.ReadFile(filepath.Join(base, "versions.txt"))
	if !strings.Contains(string(vt), "1.0.0") || !strings.Contains(string(vt), "2.0.0") {
		t.Errorf("versions.txt = %q", vt)
	}

	// second run is fully incremental (everything already present).
	o.dryRun = false
	if err := mirror([]string{"acme.org/tool"}, o); err != nil {
		t.Fatal(err)
	}
}

func TestRunHelpVersion(t *testing.T) {
	if rc := run([]string{"--help"}); rc != 0 {
		t.Errorf("--help rc=%d", rc)
	}
	if rc := run([]string{"-v"}); rc != 0 {
		t.Errorf("-v rc=%d", rc)
	}
	if rc := run(nil); rc != 2 {
		t.Errorf("no-args rc=%d", rc)
	}
}
