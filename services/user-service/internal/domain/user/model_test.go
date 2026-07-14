package user

import "testing"

func TestCanTransition(t *testing.T) {
	t.Parallel()
	tests := []struct {
		from AccountStatus
		to   AccountStatus
		want bool
	}{
		{StatusActive, StatusRestricted, true},
		{StatusRestricted, StatusActive, true},
		{StatusActive, StatusDeactivated, true},
		{StatusRestricted, StatusDeactivated, true},
		{StatusDeactivated, StatusActive, true},
		{StatusDeactivated, StatusRestricted, false},
		{StatusActive, StatusActive, false},
	}
	for _, test := range tests {
		if got := CanTransition(test.from, test.to); got != test.want {
			t.Fatalf("CanTransition(%q, %q) = %v, want %v", test.from, test.to, got, test.want)
		}
	}
}

func TestNormalizeProfileValues(t *testing.T) {
	t.Parallel()
	if got, err := NormalizeNickname("  dropfan  "); err != nil || got != "dropfan" {
		t.Fatalf("NormalizeNickname() = %q, %v", got, err)
	}
	empty := "  "
	if got, err := NormalizeIntroduction(&empty); err != nil || got != nil {
		t.Fatalf("NormalizeIntroduction() = %v, %v", got, err)
	}
}
