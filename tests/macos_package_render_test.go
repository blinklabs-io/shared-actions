package workflows

import (
	"os"
	"os/exec"
	"testing"
)

func TestMacOSPackageMetadata(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "python3", "macos_package_render_test.py")
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("macOS package metadata tests failed: %v\n%s", err, output)
	}
}
