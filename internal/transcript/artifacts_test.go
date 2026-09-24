package transcript

import "testing"

func TestFileArtifactPathsIncludesEveryPatchChange(t *testing.T) {
	read, modified := FileArtifactPaths("apply_patch", []byte(`{"changes":[{"path":"a.go"},{"path":"b.go"}]}`))
	if len(read) != 0 || len(modified) != 2 || modified[0] != "a.go" || modified[1] != "b.go" {
		t.Fatalf("read=%#v modified=%#v", read, modified)
	}
}
