package workflows

import (
	"os"
	"os/exec"
	"testing"
)

func TestMacOSPackageAssembly(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "python3", "macos_package_assembly_test.py")
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("macOS package assembly tests failed: %v\n%s", err, output)
	}
}
