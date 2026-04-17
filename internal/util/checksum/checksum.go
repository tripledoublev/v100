package checksum

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
)

// FileSHA256 calculates the SHA-256 hash of a file efficiently using io.Copy.
// It returns the hash as a hex string and any error encountered.
func FileSHA256(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}
