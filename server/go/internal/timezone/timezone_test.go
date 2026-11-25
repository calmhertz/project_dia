package timezone

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var winter = time.Date(2026, time.January, 10, 0, 0, 0, 0, time.UTC)

func TestIANANamesAreAccepted(t *testing.T) {
	for _, name := range []string{"UTC", "Asia/Kolkata", "Europe/London",
		"America/New_York", "Asia/Calcutta"} {
		if _, err := Validate(name); err != nil {
			t.Errorf("Validate(%q) = %v, want ok", name, err)
		}
	}
}

func TestAbbreviationsAndOffsetsAreRefused(t *testing.T) {
	for _, name := range []string{"", "IST", "EST", "CET", "GMT", "+05:30",
		"UTC+05:30", "+00:00", "Local", "Asia/Atlantis", "asia/kolkata",
		" Asia/Kolkata", "../etc/passwd"} {
		if _, err := Validate(name); err == nil {
			t.Errorf("Validate(%q) accepted, want refused", name)
		}
	}
}

func TestDescribeShowsDaylightSaving(t *testing.T) {
	kolkata, err := Describe("Asia/Kolkata", winter)
	if err != nil {
		t.Fatal(err)
	}
	if kolkata.Offset != "+05:30" || kolkata.Label != "Asia/Kolkata (UTC+05:30)" {
		t.Errorf("Kolkata = %+v", kolkata)
	}

	london, _ := Describe("Europe/London", winter)
	if london.Offset != "+00:00" || london.Label != "Europe/London (UTC+00:00 / +01:00)" {
		t.Errorf("London = %+v", london)
	}
	summer := time.Date(2026, time.July, 10, 0, 0, 0, 0, time.UTC)
	if london, _ := Describe("Europe/London", summer); london.Offset != "+01:00" {
		t.Errorf("London in summer = %s, want +01:00", london.Offset)
	}
	if newYork, _ := Describe("America/New_York", winter); newYork.Offset != "-05:00" {
		t.Errorf("New York = %s, want -05:00", newYork.Offset)
	}
}

func TestListReadsTheZoneTable(t *testing.T) {
	dir := t.TempDir()
	table := "# comment\n" +
		"IN\t+2232+08822\tAsia/Kolkata\n" +
		"GB,GG,IM,JE\t+513030-0000731\tEurope/London\n" +
		"US\t+404251-0740023\tAmerica/New_York\tEastern (most areas)\n" +
		"XX\t+0000+00000\tNowhere/Imaginary\n"
	if err := os.WriteFile(filepath.Join(dir, "zone1970.tab"), []byte(table), 0o644); err != nil {
		t.Fatal(err)
	}

	zones, err := List(dir, winter)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, zone := range zones {
		names = append(names, zone.Name)
	}
	want := []string{"America/New_York", "Europe/London", "UTC", "Asia/Kolkata"}
	if len(names) != len(want) {
		t.Fatalf("zones = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("zones = %v, want %v (ordered by offset, unknown zones dropped)", names, want)
		}
	}
}

func TestAMissingZoneTableIsReported(t *testing.T) {
	if _, err := List(t.TempDir(), winter); !errors.Is(err, ErrNoZoneList) {
		t.Errorf("error = %v, want ErrNoZoneList", err)
	}
}

func TestTheSystemZoneTableLoads(t *testing.T) {
	if _, err := os.Stat(filepath.Join(DefaultDirectory, "zone1970.tab")); err != nil {
		t.Skip("no system zone table on this machine")
	}
	zones, err := List(DefaultDirectory, winter)
	if err != nil {
		t.Fatal(err)
	}
	if len(zones) < 250 {
		t.Errorf("only %d zones from the system table", len(zones))
	}
}
