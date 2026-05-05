package cli

// Version is set at build time via:
//
//	go build -ldflags "-X github.com/openrelik/openrelik-cli/cli.Version=1.2.3" ./
var Version = "dev"
