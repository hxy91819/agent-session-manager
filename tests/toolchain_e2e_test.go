package tests

import (
	"debug/buildinfo"
	"go/version"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestReleaseBinaryUsesPatchedGoToolchain(t *testing.T) {
	const minimumGoVersion = "go1.26.5"

	binary := filepath.Join(t.TempDir(), "asm")
	cmd := exec.Command("go", "build", "-o", binary, "./cmd/asm")
	cmd.Dir = ".."
	cmd.Env = append(os.Environ(), "GO111MODULE=on", "GOTOOLCHAIN=auto")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build asm: %v\n%s", err, out)
	}

	info, err := buildinfo.ReadFile(binary)
	if err != nil {
		t.Fatalf("read build info: %v", err)
	}
	if version.Compare(info.GoVersion, minimumGoVersion) < 0 {
		t.Fatalf("binary Go version = %s, want %s or newer", info.GoVersion, minimumGoVersion)
	}
}
