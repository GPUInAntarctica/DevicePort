// Minimal module file used only for building the Windows release binary.
// Go 1.23 is used because Go 1.26's UDP socket path is not supported by Wine.
// The main module (go.mod) requires Go 1.26 for the gomobile tool directive.
module github.com/GPUInAntarctica/DevicePort

go 1.23
