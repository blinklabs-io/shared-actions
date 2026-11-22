package workflows

import (
	"os"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestBinaryPublishingUsesOnlyRequiredTokenPermissions(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/reuseable-publish.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Permissions map[string]string
		}
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"contents": "write", "id-token": "write", "attestations": "write"}
	if got := workflow.Jobs["build-binaries"].Permissions; !reflect.DeepEqual(got, want) {
		t.Fatalf("binary uploads and attestations require %v, got %v", want, got)
	}
}
