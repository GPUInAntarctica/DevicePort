// Command deviceport-server is a compatibility alias for
// `deviceport server`. The single-file executable is the preferred entry point.
package main

import (
	"os"

	"github.com/GPUInAntarctica/DevicePort/internal/cli"
)

func main() { cli.RunServer(os.Args[1:]) }
