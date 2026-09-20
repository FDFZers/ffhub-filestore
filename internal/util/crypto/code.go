package crypto

import (
	"crypto/rand"
	"math/big"
)

const codeCharset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func GenerateCode(length int) (string, error) {
	result := make([]byte, length)
	maxIndex := big.NewInt(int64(len(codeCharset)))

	for i := range length {
		idx, err := rand.Int(rand.Reader, maxIndex)
		if err != nil {
			return "", err
		}
		result[i] = codeCharset[idx.Int64()]
	}
	return string(result), nil
}
