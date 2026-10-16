//go:build windows && (amd64 || arm64)

package winput

import "unsafe"

// Compile-time layout checks against the Win64 ABI: INPUT is 40 bytes,
// KBDLLHOOKSTRUCT 24, MSLLHOOKSTRUCT 32 and NOTIFYICONDATAW 976. A mismatch fails the build.
var (
	_ = [1]struct{}{}[unsafe.Sizeof(input{})-40]
	_ = [1]struct{}{}[unsafe.Sizeof(kbdll{})-24]
	_ = [1]struct{}{}[unsafe.Sizeof(msll{})-32]
	_ = [1]struct{}{}[unsafe.Sizeof(notifyIconData{})-976]
)
