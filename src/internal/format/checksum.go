package format

import "hash/crc32"

// castagnoli is the CRC32C table. hash/crc32 uses SSE4.2 / ARMv8 CRC
// instructions for this polynomial when available.
var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// Checksum returns the CRC32C (Castagnoli) of b.
func Checksum(b []byte) uint32 { return crc32.Checksum(b, castagnoli) }

// UpdateChecksum continues a running CRC32C with b. Starting from 0 and
// feeding the data in any number of pieces gives the same result as Checksum
// over the concatenation; this is what makes streaming encode/decode possible.
func UpdateChecksum(crc uint32, b []byte) uint32 { return crc32.Update(crc, castagnoli, b) }
