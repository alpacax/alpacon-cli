package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
)

var ErrChecksumMismatch = errors.New("checksum mismatch")

func ParseChecksums(data []byte) map[string]string { // Each line is "<sha256>  <file name>", the shape goreleaser writes.
	sums := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		sums[fields[1]] = fields[0]
	}
	return sums
}

func VerifyChecksum(path, want string) error {
	if want == "" { // An empty expectation is a checksum file that never named this archive—no weaker a reason to stop than a hash that disagrees.
		return ErrChecksumMismatch
	}

	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}

	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), want) {
		return ErrChecksumMismatch
	}
	return nil
}
