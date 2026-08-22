package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const generictestPkg = "github.com/arl/sockdrawer/generictest"

func TestGenerictestSplit(t *testing.T) {
	repoRoot := repoRoot(t)
	clusterFile := filepath.Join(t.TempDir(), "box.clusters")
	writeFile(t, clusterFile, `= github.com/arl/sockdrawer/generictest/box
Box
`)

	printOutput := runSockdrawer(t, repoRoot, "-print", "-clusters="+clusterFile, generictestPkg)
	for _, want := range []string{
		"# 14 nodes in 2 clusters",
		"= github.com/arl/sockdrawer/generictest/box",
		"Box                                     # generics.go:9",
		"(github.com/arl/sockdrawer/generictest.Box[T]).Unwrap# generics.go:17",
		"= residue",
		"BuildCounter                            # usage.go:13",
		"Counter                                 # usage.go:5",
	} {
		if !strings.Contains(printOutput, want) {
			t.Fatalf("print output missing %q:\n%s", want, printOutput)
		}
	}

	outdir := filepath.Join(t.TempDir(), "split")
	runSockdrawer(t, repoRoot, "-clusters="+clusterFile, "-outdir="+outdir, generictestPkg)

	mustFileEquals(t, filepath.Join(outdir, "github.com/arl/sockdrawer/generictest/box", "generics.go"), `package box

type Box[T any] struct {
	Value T
}

func (b Box[T]) Unwrap() T {
	return b.Value
}
`)
	mustFileEquals(t, filepath.Join(outdir, "residue", "generics.go"), `package residue

import (
	"fmt"
	_box "github.com/arl/sockdrawer/generictest/box"
)

const PackageName = "generictest"

var DefaultBox = NewBox(1)

func NewBox[T any](v T) _box.Box[T] {
	return _box.Box[T]{Value: v}
}

type Pair[A, B any] struct {
	First  A
	Second B
}

func MakePair[A, B any](a A, b B) Pair[A, B] {
	return Pair[A, B]{First: a, Second: b}
}

type Number interface {
	~int | ~int64
}

func Sum[T Number](values []T) T {
	var total T
	for _, v := range values {
		total += v
	}
	return total
}

func Describe[T any](v T) string {
	return fmt.Sprintf("%s:%v", PackageName, v)
}
`)
	mustFileEquals(t, filepath.Join(outdir, "residue", "usage.go"), `package residue

import (
	"fmt"
)

type Counter struct {
	N int
}

func (c *Counter) Inc() {
	c.N++
}

func BuildCounter() *Counter {
	c := &Counter{N: len(fmt.Sprint(DefaultBox.Unwrap()))}
	c.Inc()
	return c
}

func (c Counter) String() string {
	return Describe(c.N)
}
`)
}

func runSockdrawer(t *testing.T, repoRoot string, args ...string) string {
	t.Helper()

	cmdArgs := append([]string{"run", "."}, args...)
	cmd := exec.Command("go", cmdArgs...)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "GO111MODULE=on")

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go %s failed: %v\n%s", strings.Join(cmdArgs, " "), err, out)
	}
	return string(out)
}

func mustFileEquals(t *testing.T, path, want string) {
	t.Helper()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("unexpected contents of %s:\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	return wd
}
