package workflows

import (
	"os"
	"os/exec"
	"testing"
)

func TestMacOSBinaryBuild(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "python3", "macos_build_test.py")
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("macOS binary build tests failed: %v\n%s", err, output)
	}
}
