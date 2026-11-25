package workflows

import (
	"os"
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
		if value, ok := step.Env["SIGN_STORE_TYPE"]; ok {
			got = value
		}
	}
	if got != "${{ inputs.store-type }}" {
		t.Errorf("SIGN_STORE_TYPE = %q, want store-type input", got)
	}
}
