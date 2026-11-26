package app

import "os"

func isRunningInContainer() bool {
	_, dockerErr := os.Stat("/.dockerenv")
	_, podmanErr := os.Stat("/run/.containerenv")
	// Apple's container runtime exposes vsock to its Linux guests instead of
	// creating either of the marker files above.
	_, vsockErr := os.Stat("/dev/vsock")
	return dockerErr == nil || podmanErr == nil || vsockErr == nil
}
