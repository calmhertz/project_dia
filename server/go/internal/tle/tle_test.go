package tle

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// Captured from CelesTrak on 2026-08-24. Real data, including the CRLF line
// endings and the space-padded title that the live endpoint returns.
const celestrakISS = "ISS (ZARYA)             \r\n" +
	"1 25544U 98067A   26236.17729445  .00008773  00000+0  16369-3 0  9990\r\n" +
	"2 25544  51.6333 323.5788 0007697  77.8713 282.3138 15.49600847582298\r\n"

// Captured from SatNOGS on the same day. Note the "0 " title prefix and the
// slightly different B* formatting, which changes the checksum digit.
const satnogsISS = "0 ISS (ZARYA)\n" +
	"1 25544U 98067A   26236.17729445  .00008773  00000-0  16369-3 0  9991\n" +
	"2 25544  51.6333 323.5788 0007697  77.8713 282.3138 15.49600847582298\n"

const celestrakNOAA19 = "NOAA 19                 \r\n" +
	"1 33591U 09005A   26236.28857781  .00000013  00000+0  30729-4 0  9998\r\n" +
	"2 33591  98.9473 307.1961 0013555 193.3958 166.6856 14.13483201904106\r\n"

func TestParseRealCelestrakResponse(t *testing.T) {
	set, err := Parse(celestrakISS)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if set.NoradID != 25544 {
		t.Errorf("NoradID = %d, want 25544", set.NoradID)
	}
	// The padded title must be trimmed, and CRLF must not survive into storage.
	if set.Name != "ISS (ZARYA)" {
		t.Errorf("Name = %q, want %q", set.Name, "ISS (ZARYA)")
	}
	if strings.ContainsAny(set.Line1+set.Line2, "\r\n") {
		t.Error("stored lines still carry line endings")
	}
	if len(set.Line1) != 69 || len(set.Line2) != 69 {
		t.Errorf("line lengths = %d, %d; want 69", len(set.Line1), len(set.Line2))
	}
}

func TestParseRealSatNOGSResponse(t *testing.T) {
	set, err := Parse(satnogsISS)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if set.NoradID != 25544 {
		t.Errorf("NoradID = %d, want 25544", set.NoradID)
	}
	// The 3LE "0 " prefix is a format marker, not part of the name.
	if set.Name != "ISS (ZARYA)" {
		t.Errorf("Name = %q, want %q", set.Name, "ISS (ZARYA)")
	}
}

// Both providers describe the same orbit at the same epoch even though the
// exact text differs, so the epoch must agree.
func TestBothProvidersAgreeOnEpoch(t *testing.T) {
	fromCelestrak, err := Parse(celestrakISS)
	if err != nil {
		t.Fatalf("celestrak: %v", err)
	}
	fromSatNOGS, err := Parse(satnogsISS)
	if err != nil {
		t.Fatalf("satnogs: %v", err)
	}

	if !fromCelestrak.Epoch.Equal(fromSatNOGS.Epoch) {
		t.Errorf("epochs differ: %s vs %s", fromCelestrak.Epoch, fromSatNOGS.Epoch)
	}
	// The text differs, so they are genuinely distinct stored versions.
	if fromCelestrak.SameElements(fromSatNOGS) {
		t.Error("expected the two providers' text to differ")
	}
}

func TestEpochDecoding(t *testing.T) {
	set, err := Parse(celestrakISS)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// 26236.17729445 is day 236 of 2026, plus 0.17729445 of a day.
	want := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC).
		Add(time.Duration(235.17729445 * float64(24*time.Hour)))

	if difference := set.Epoch.Sub(want); difference > time.Second || difference < -time.Second {
		t.Errorf("Epoch = %s, want about %s", set.Epoch, want)
	}
	if set.Epoch.Year() != 2026 {
		t.Errorf("Epoch year = %d, want 2026", set.Epoch.Year())
	}
}

// The two-digit year rolls over at 57 per the TLE convention.
func TestEpochYearRollover(t *testing.T) {
	cases := map[string]int{"57001.00000000": 1957, "99001.00000000": 1999, "00001.00000000": 2000, "56001.00000000": 2056}

	for field, wantYear := range cases {
		line := "1 25544U 98067A   " + field + "  .00008773  00000+0  16369-3 0  999"
		line += string(rune('0' + Checksum(line)))

		epoch, err := ParseEpoch(line)
		if err != nil {
			t.Fatalf("ParseEpoch(%s): %v", field, err)
		}
		if epoch.Year() != wantYear {
			t.Errorf("epoch %s -> year %d, want %d", field, epoch.Year(), wantYear)
		}
	}
}

// The modulo-10 checksum is the only integrity check a TLE carries.
func TestChecksumRejectsCorruptedLines(t *testing.T) {
	set, err := Parse(celestrakISS)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Flip a digit in the middle of line 1; the checksum must catch it.
	corrupted := []byte(set.Line1)
	if corrupted[20] == '9' {
		corrupted[20] = '8'
	} else {
		corrupted[20] = '9'
	}

	if err := ValidateLine(string(corrupted), '1'); !errors.Is(err, ErrChecksum) {
		t.Errorf("corrupted line error = %v, want ErrChecksum", err)
	}
}

func TestChecksumCountsMinusSignsAsOne(t *testing.T) {
	// "16369-3" contributes its digits plus 1 for the minus.
	if got := Checksum("-"); got != 1 {
		t.Errorf("Checksum(\"-\") = %d, want 1", got)
	}
	if got := Checksum("11-"); got != 3 {
		t.Errorf("Checksum(\"11-\") = %d, want 3", got)
	}
	// Letters and spaces contribute nothing.
	if got := Checksum("AB C"); got != 0 {
		t.Errorf("Checksum(\"AB C\") = %d, want 0", got)
	}
}

func TestRealLinesPassTheirOwnChecksums(t *testing.T) {
	for name, text := range map[string]string{
		"celestrak iss":  celestrakISS,
		"satnogs iss":    satnogsISS,
		"celestrak noaa": celestrakNOAA19,
	} {
		if _, err := Parse(text); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestMalformedInputIsRejected(t *testing.T) {
	cases := map[string]string{
		"empty":            "",
		"one line":         "1 25544U 98067A   26236.17729445  .00008773  00000+0  16369-3 0  9990",
		"four lines":       celestrakISS + "3 extra line\n",
		"not a tle":        "hello\nworld\n",
		"line 2 missing":   "ISS\n1 25544U 98067A   26236.17729445  .00008773  00000+0  16369-3 0  9990\n",
		"wrong line order": "2 25544  51.6333 323.5788 0007697  77.8713 282.3138 15.49600847582298\n1 25544U 98067A   26236.17729445  .00008773  00000+0  16369-3 0  9990\n",
		"truncated":        "1 25544U\n2 25544\n",
	}

	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(text); err == nil {
				t.Error("expected a parse error")
			}
		})
	}
}

// Two lines from different satellites must never be accepted as one set.
func TestMismatchedCatalogNumbersAreRejected(t *testing.T) {
	iss, _ := Parse(celestrakISS)
	noaa, _ := Parse(celestrakNOAA19)

	_, err := Parse(iss.Line1 + "\n" + noaa.Line2 + "\n")
	if err == nil || !strings.Contains(err.Error(), "catalog numbers differ") {
		t.Errorf("error = %v, want a catalog number mismatch", err)
	}
}

func TestAge(t *testing.T) {
	set, err := Parse(celestrakISS)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	now := set.Epoch.Add(30 * time.Hour)

	if age := set.Age(now); age != 30*time.Hour {
		t.Errorf("Age = %v, want 30h", age)
	}
}
