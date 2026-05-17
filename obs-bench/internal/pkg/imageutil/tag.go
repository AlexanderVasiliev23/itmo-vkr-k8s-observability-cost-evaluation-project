package imageutil

import (
	"encoding/hex"
	"fmt"

	"golang.org/x/mod/sumdb/dirhash"
)

// BuildDevTag строит детерминированный тег образа по хешу директории с Dockerfile.
// Если namespace непустой, тег будет вида "namespace/name:dev-xxx".
func BuildDevTag(contextPath, imageName, namespace string) (string, error) {
	hash, err := dirhash.HashDir(contextPath, "", dirhash.Hash1)
	if err != nil {
		return "", fmt.Errorf("hash %s: %w", contextPath, err)
	}
	hexHash := hex.EncodeToString([]byte(hash))
	name := fmt.Sprintf("%s:dev-%s", imageName, hexHash[:12])

	if namespace == "" {
		return "", fmt.Errorf("namespace must not be empty")
	}

	return namespace + "/" + name, nil
}
