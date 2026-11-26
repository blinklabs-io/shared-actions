package workflows

import (
	"os"
	"os/exec"
	"testing"
)

func TestMacOSCLIZip(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "python3", "macos_cli_zip_test.py")
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("macOS CLI ZIP tests failed: %v\n%s", err, output)
	}
}
