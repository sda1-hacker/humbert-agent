package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sda1-hacker/humbert-agent/internal/instancelock"
)

func TestDataCLIHelper(t *testing.T) {
	mode := os.Getenv("HUMBERT_DATA_CLI_TEST")
	if mode == "" {
		return
	}
	root := os.Getenv("HUMBERT_DATA_CLI_ROOT")
	os.Args = []string{"humbert-data", mode, "-data-dir", root, "-offline", "-passphrase-file", filepath.Join(root, "missing-password")}
	if mode == "backup" {
		os.Args = append(os.Args, "-output", filepath.Join(root, "backup.age"))
	} else {
		os.Args = append(os.Args, "-archive", filepath.Join(root, "backup.age"))
	}
	main()
}

func TestDataCommandsRejectLiveDataDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	lock, err := instancelock.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	for _, mode := range []string{"backup", "restore"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestDataCLIHelper$")
		cmd.Env = append(os.Environ(), "HUMBERT_DATA_CLI_TEST="+mode, "HUMBERT_DATA_CLI_ROOT="+root)
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), instancelock.ErrInUse.Error()) {
			t.Fatalf("%s accessed live data before checking lock: %v %s", mode, err, out)
		}
	}
}
