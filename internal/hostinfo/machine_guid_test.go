package hostinfo

import "testing"

func TestNormalizeMachineGUID(t *testing.T) {
	got, err := normalizeMachineGUID("  6f0c2f3e-1a2b-4c5d-8e9f-0123456789ab\n")
	if err != nil {
		t.Fatal(err)
	}
	if want := "6f0c2f3e-1a2b-4c5d-8e9f-0123456789ab"; got != want {
		t.Fatalf("normalizeMachineGUID = %q, want %q", got, want)
	}

	for _, value := range []string{"", "   ", "\t\n"} {
		if _, err := normalizeMachineGUID(value); err == nil {
			t.Fatalf("normalizeMachineGUID(%q) = nil error, want error", value)
		}
	}
}
