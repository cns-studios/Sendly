package models

import "testing"

func TestIsValidFileID(t *testing.T) {
	for i := 0; i < 100; i++ {
		if id := GenerateID(FileIDLength); !IsValidFileID(id) {
			t.Fatalf("generated id %q is not valid", id)
		}
	}
	valid := []string{"abcdefghijklmnopq", "0000000000000000z", "k3x9q2m8v1p0z7r4t"}
	for _, id := range valid {
		if !IsValidFileID(id) {
			t.Errorf("%q should be valid", id)
		}
	}
	invalid := []string{"", "abc", "abcdefghijklmnopqr", "Abcdefghijklmnopq", "abcdefghijklmnop-", "abcdefghijklmnop_", "abcdefghijklmnop/", "abcdefghijklmnopé"}
	for _, id := range invalid {
		if IsValidFileID(id) {
			t.Errorf("%q should be invalid", id)
		}
	}
}
