package identity

import "testing"

func TestNewUUIDProducesValidUUID(t *testing.T) {
	value, err := NewUUID()
	if err != nil {
		t.Fatalf("NewUUID returned an error: %v", err)
	}
	if !IsUUID(value) {
		t.Fatalf("NewUUID returned invalid value %q", value)
	}
}

func TestIsUUIDRejectsMalformedValues(t *testing.T) {
	for _, value := range []string{
		"",
		"not-a-uuid",
		"00000000-0000-0000-0000-00000000000g",
		"000000000000-0000-0000-000000000000",
	} {
		if IsUUID(value) {
			t.Errorf("IsUUID accepted %q", value)
		}
	}
}
