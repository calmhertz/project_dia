// Package timezone validates and lists IANA time zones.
//
// Stations store an IANA name such as "Asia/Kolkata". Abbreviations ("IST"
// means India, Ireland or Israel) and fixed offsets ("+05:30" ignores daylight
// saving) are refused: a station is a place, and only a zone name describes a
// place's clock all year round.
//
// The list of selectable zones and the rules behind every offset both come
// from the system's tz database (the host's /usr/share/zoneinfo, mounted into
// the container), so a Debian tzdata update reaches the station without a
// rebuild. The copy of the database built into the binary is only a fallback
// for validation when the system files are missing.
package timezone

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	_ "time/tzdata" // fallback when the system has no zoneinfo
)

// DefaultDirectory is where the system keeps the tz database.
const DefaultDirectory = "/usr/share/zoneinfo"

// InvalidMessage explains what a valid time zone looks like.
const InvalidMessage = `timezone must be an IANA time zone name such as "Asia/Kolkata", ` +
	`"Europe/London" or "UTC"; abbreviations like IST and offsets like +05:30 are not accepted`

// ErrNoZoneList means the system tz database has no zone list to offer.
var ErrNoZoneList = errors.New("the system time zone list is unavailable")

// Zone is a selectable time zone with its UTC offsets.
type Zone struct {
	Name string `json:"name"`
	// Offset is the UTC offset in effect now, such as "+05:30".
	Offset string `json:"offset"`
	// OffsetSeconds is Offset in seconds.
	OffsetSeconds int `json:"offset_seconds"`
	// Label reads "Europe/London (UTC+00:00 / +01:00)", giving both offsets
	// when the zone observes daylight saving.
	Label string `json:"label"`
}

// Directory returns the tz database directory: $ZONEINFO when it names a
// directory (Go's own lookup honours the same variable), else the default.
func Directory() string {
	if dir := os.Getenv("ZONEINFO"); dir != "" {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return DefaultDirectory
}

// Validate reports whether name is an IANA zone and returns its location.
//
// Any name in the database is accepted, including older aliases such as
// "Asia/Calcutta", but not bare abbreviations or offsets, even the few
// ("EST", "CET") the database still carries for compatibility.
func Validate(name string) (*time.Location, error) {
	if name != strings.TrimSpace(name) || name == "Local" ||
		(name != "UTC" && !strings.Contains(name, "/")) {
		return nil, errors.New(InvalidMessage)
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return nil, errors.New(InvalidMessage)
	}
	return location, nil
}

// List reads the canonical zones from zone1970.tab in dir, plus UTC, ordered
// by the offset in effect at now, then by name.
func List(dir string, now time.Time) ([]Zone, error) {
	names, err := readZoneTab(filepath.Join(dir, "zone1970.tab"))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoZoneList, err)
	}
	names = append(names, "UTC")

	seen := make(map[string]bool, len(names))
	zones := make([]Zone, 0, len(names))
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		zone, err := Describe(name, now)
		if err != nil {
			continue // listed but not loadable; never offer it
		}
		zones = append(zones, zone)
	}
	if len(zones) == 0 {
		return nil, ErrNoZoneList
	}
	sort.Slice(zones, func(i, j int) bool {
		if zones[i].OffsetSeconds != zones[j].OffsetSeconds {
			return zones[i].OffsetSeconds < zones[j].OffsetSeconds
		}
		return zones[i].Name < zones[j].Name
	})
	return zones, nil
}

// readZoneTab returns the zone names (third column) of a zone1970.tab file.
func readZoneTab(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var names []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) >= 3 && fields[2] != "" {
			names = append(names, fields[2])
		}
	}
	return names, scanner.Err()
}

// Describe returns one zone's offsets at the given moment.
func Describe(name string, now time.Time) (Zone, error) {
	location, err := Validate(name)
	if err != nil {
		return Zone{}, err
	}
	_, current := now.In(location).Zone()

	year := now.Year()
	_, january := time.Date(year, time.January, 15, 12, 0, 0, 0, time.UTC).In(location).Zone()
	_, july := time.Date(year, time.July, 15, 12, 0, 0, 0, time.UTC).In(location).Zone()

	offsets := "UTC" + formatOffset(min(january, july))
	if january != july {
		offsets += " / " + formatOffset(max(january, july))
	}
	return Zone{
		Name:          name,
		Offset:        formatOffset(current),
		OffsetSeconds: current,
		Label:         fmt.Sprintf("%s (%s)", name, offsets),
	}, nil
}

func formatOffset(seconds int) string {
	sign := '+'
	if seconds < 0 {
		sign = '-'
		seconds = -seconds
	}
	return fmt.Sprintf("%c%02d:%02d", sign, seconds/3600, (seconds%3600)/60)
}
