package version

import "testing"

func TestCurrent(t *testing.T) {
	t.Parallel()

	info := Current("coordinator")
	if info.Service != "coordinator" {
		t.Fatalf("Service = %q, want coordinator", info.Service)
	}
	if info.Version == "" || info.Commit == "" || info.BuildTime == "" {
		t.Fatal("Current() returned incomplete build information")
	}
}
