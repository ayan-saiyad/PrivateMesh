// Package version exposes service build information.
package version

// Build values are replaced through linker flags in release builds.
var (
	Version   = "dev"
	Commit    = "none"
	BuildTime = "unknown"
)

// Info identifies a running service binary.
type Info struct {
	Service   string `json:"service"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
}

// Current returns the build identity for a service.
func Current(service string) Info {
	return Info{
		Service:   service,
		Version:   Version,
		Commit:    Commit,
		BuildTime: BuildTime,
	}
}
