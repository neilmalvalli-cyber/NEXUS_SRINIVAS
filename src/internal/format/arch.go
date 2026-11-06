package format

// The engine supports 64-bit targets only (amd64, arm64). File positions are
// uint64, index locations hold u64 offsets, and slices of up to MaxEntrySize
// bytes must be addressable. On a 32-bit target `uint` is 32 bits wide and the
// constant below overflows, so the build fails instead of silently truncating
// offsets at runtime.
const _ uint = 1 << 63
