package workflows

import (
	"os"
	"os/exec"
	"testing"
)

func TestMacOSPackageVerification(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "python3", "macos_package_verify_test.py")
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("macOS package verification tests failed: %v\n%s", err, output)
	}
}
