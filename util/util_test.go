package util

import (
	"testing"
)

func TestGenerateUUID(t *testing.T) {
	uuid := GenerateUUID()
	if len(uuid) != 32 {
		t.Errorf("GenerateUUID() = %q, expected length 32", uuid)
	}

	// Ensure it only contains hex characters
	for _, c := range uuid {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("GenerateUUID() = %q contains non-hex character %q", uuid, c)
		}
	}

	// Ensure another call gives a different UUID
	uuid2 := GenerateUUID()
	if uuid == uuid2 {
		t.Errorf("GenerateUUID() returned the same UUID twice: %q", uuid)
	}
}
