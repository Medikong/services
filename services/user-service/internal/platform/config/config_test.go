package config

import "testing"

func TestParseRequiredAgreements(t *testing.T) {
	t.Parallel()
	got, err := parseRequiredAgreements("TERMS_OF_SERVICE:2026-07-01,PRIVACY:3")
	if err != nil {
		t.Fatal(err)
	}
	if got["TERMS_OF_SERVICE"] != "2026-07-01" || got["PRIVACY"] != "3" {
		t.Fatalf("unexpected agreements: %#v", got)
	}
	if _, err := parseRequiredAgreements("TERMS_OF_SERVICE:1,TERMS_OF_SERVICE:2"); err == nil {
		t.Fatal("duplicate agreement was accepted")
	}
}

func TestDevelopmentFeaturesAreEnvironmentBound(t *testing.T) {
	t.Setenv("USER_DEVELOPMENT_ENABLED", "true")
	t.Setenv("USER_DEV_ACCESS_TOKEN", "development-token")
	if _, err := loadDevelopment("production", "private", "private"); err == nil {
		t.Fatal("production development routes were accepted")
	}
}
