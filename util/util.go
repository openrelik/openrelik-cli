package util

import (
	"strings"

	"github.com/google/uuid"
)

// GenerateUUID generates a random UUID v4 (hex, no hyphens).
func GenerateUUID() string {
	return strings.ReplaceAll(uuid.New().String(), "-", "")
}
