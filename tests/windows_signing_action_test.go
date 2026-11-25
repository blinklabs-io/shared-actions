package workflows

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestWindowsSigningStoreTypeContract(t *testing.T) {
	data, err := os.ReadFile("../sign-windows/action.yml")
	if err != nil {
		t.Fatal(err)
	}
	var action struct {
		Inputs map[string]struct {
			Default string `yaml:"default"`
		} `yaml:"inputs"`
		Runs struct {
			Steps []struct {
				Env map[string]string `yaml:"env"`
				Run string            `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"runs"`
	}
	if err := yaml.Unmarshal(data, &action); err != nil {
		t.Fatal(err)
	}
	if got := action.Inputs["store-type"].Default; got != "GOOGLECLOUD" {
		t.Errorf("store-type default = %q, want GOOGLECLOUD", got)
	}
	var got string
	for _, step := range action.Runs.Steps {
		if strings.Contains(step.Run, "sign.ps1") {
			got = step.Env["SIGN_STORE_TYPE"]
		}
	}
	if got != "${{ inputs.store-type }}" {
		t.Errorf("SIGN_STORE_TYPE = %q, want store-type input", got)
	}
}
