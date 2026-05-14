package version

// Version is set at build time via ldflags:
//
//	-X github.com/jpvelasco/fabrica/internal/version.Version=v1.0.0
var Version = "dev"

// Commit is set at build time via ldflags:
//
//	-X github.com/jpvelasco/fabrica/internal/version.Commit=abc1234
var Commit = "unknown"
