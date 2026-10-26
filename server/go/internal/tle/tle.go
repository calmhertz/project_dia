package tle

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	lineLength    = 69
	minLineLength = 68
)

var (
	ErrMalformed = errors.New("malformed TLE")
	ErrChecksum = errors.New("TLE checksum mismatch")
)

type Set struct {
	Name string
	NoradID int
	Line1   string
	Line2   string
	Epoch   time.Time
}

func Parse(text string) (Set, error) {
	var lines []string
	for _, raw := range strings.Split(text, "\n") {
		trimmed := strings.TrimRight(raw, " \t\r")
		if trimmed != "" {
			lines = append(lines, trimmed)
		}
	}

	switch len(lines) {
	case 2:
		return newSet("", lines[0], lines[1])
	case 3:
		return newSet(strings.TrimPrefix(lines[0], "0 "), lines[1], lines[2])
	default:
		return Set{}, fmt.Errorf("%w: expected 2 or 3 lines, got %d", ErrMalformed, len(lines))
	}
}

func newSet(name, line1, line2 string) (Set, error) {
	if err := ValidateLine(line1, '1'); err != nil {
		return Set{}, fmt.Errorf("line 1: %w", err)
	}
	if err := ValidateLine(line2, '2'); err != nil {
		return Set{}, fmt.Errorf("line 2: %w", err)
	}

	noradID, err := catalogNumber(line1)
	if err != nil {
		return Set{}, err
	}
	secondID, err := catalogNumber(line2)
	if err != nil {
		return Set{}, err
	}
	if noradID != secondID {
		return Set{}, fmt.Errorf("%w: catalog numbers differ (%d and %d)", ErrMalformed, noradID, secondID)
	}

	epoch, err := ParseEpoch(line1)
	if err != nil {
		return Set{}, err
	}

	return Set{
		Name:    strings.TrimSpace(name),
		NoradID: noradID,
		Line1:   line1,
		Line2:   line2,
		Epoch:   epoch,
	}, nil
}

func ValidateLine(line string, want byte) error {
	if len(line) < minLineLength || len(line) > lineLength {
		return fmt.Errorf("%w: length %d, expected %d", ErrMalformed, len(line), lineLength)
	}
	if line[0] != want {
		return fmt.Errorf("%w: starts with %q, expected %q", ErrMalformed, line[0], want)
	}
	if line[1] != ' ' {
		return fmt.Errorf("%w: missing separator after line number", ErrMalformed)
	}

	expected, err := strconv.Atoi(string(line[len(line)-1]))
	if err != nil {
		return fmt.Errorf("%w: checksum is not a digit", ErrMalformed)
	}
	if actual := Checksum(line[:len(line)-1]); actual != expected {
		return fmt.Errorf("%w: computed %d, line says %d", ErrChecksum, actual, expected)
	}
	return nil
}

func Checksum(body string) int {
	sum := 0
	for _, character := range body {
		switch {
		case character >= '0' && character <= '9':
			sum += int(character - '0')
		case character == '-':
			sum++
		}
	}
	return sum % 10
}

func catalogNumber(line string) (int, error) {
	field := strings.TrimSpace(line[2:7])
	value, err := strconv.Atoi(field)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%w: bad catalog number %q", ErrMalformed, field)
	}
	return value, nil
}

func ParseEpoch(line1 string) (time.Time, error) {
	if len(line1) < 32 {
		return time.Time{}, fmt.Errorf("%w: line 1 too short for an epoch", ErrMalformed)
	}
	field := strings.TrimSpace(line1[18:32])

	year, err := strconv.Atoi(field[:2])
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: bad epoch year", ErrMalformed)
	}
	if year < 57 {
		year += 2000
	} else {
		year += 1900
	}

	dayOfYear, err := strconv.ParseFloat(field[2:], 64)
	if err != nil || dayOfYear < 1 {
		return time.Time{}, fmt.Errorf("%w: bad epoch day %q", ErrMalformed, field[2:])
	}

	start := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)
	offset := time.Duration((dayOfYear - 1) * float64(24*time.Hour))
	return start.Add(offset).UTC(), nil
}

func (s Set) Age(now time.Time) time.Duration {
	return now.Sub(s.Epoch)
}

func (s Set) SameElements(other Set) bool {
	return s.Line1 == other.Line1 && s.Line2 == other.Line2
}
