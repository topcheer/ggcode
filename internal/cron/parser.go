package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// cronField represents a parsed cron field as a set of matching values.
type cronField struct {
	values map[int]bool // which values match
	min    int          // domain minimum (e.g. 0 for minute)
	max    int          // domain maximum (e.g. 59 for minute)
}

// NextTime calculates the next time that matches the 5-field cron expression
// after the given 'from' time. Returns the next fire time or an error.
//
// Supported syntax per field:
//   - *        — any value
//   - */N      — every Nth value
//   - N        — exact value
//   - N,M,K    — list of values
//   - N-M      — range (inclusive)
//   - N-M/S    — range with step
func NextTime(expr string, from time.Time) (time.Time, error) {
	fields := splitFields(expr)
	if len(fields) != 5 {
		return time.Time{}, fmt.Errorf("cron expression must have 5 fields, got %d", len(fields))
	}

	minute, err := parseField(fields[0], 0, 59)
	if err != nil {
		return time.Time{}, fmt.Errorf("minute field: %w", err)
	}
	hour, err := parseField(fields[1], 0, 23)
	if err != nil {
		return time.Time{}, fmt.Errorf("hour field: %w", err)
	}
	day, err := parseField(fields[2], 1, 31)
	if err != nil {
		return time.Time{}, fmt.Errorf("day-of-month field: %w", err)
	}
	month, err := parseField(fields[3], 1, 12)
	if err != nil {
		return time.Time{}, fmt.Errorf("month field: %w", err)
	}
	dow, err := parseField(fields[4], 0, 6) // 0=Sunday
	if err != nil {
		return time.Time{}, fmt.Errorf("day-of-week field: %w", err)
	}

	// Start from the next second after 'from'.
	t := time.Date(from.Year(), from.Month(), from.Day(), from.Hour(), from.Minute(), 0, 0, from.Location()).Add(time.Minute)

	// Search forward, cap at ~4 years to prevent infinite loops.
	dayRestricted := !isWildcard(fields[2])
	dowRestricted := !isWildcard(fields[4])

	deadline := from.AddDate(4, 0, 0)
	for t.Before(deadline) {
		if !month.values[int(t.Month())] {
			t = firstOfNextMonth(t)
			continue
		}

		// Day matching: standard cron semantics.
		// If both day-of-month and day-of-week are restricted: OR (either matches).
		// If only one is restricted: only that one matters.
		dayMatch := day.values[t.Day()]
		dowMatch := dow.values[int(t.Weekday())]
		var dayOK bool
		if dayRestricted && dowRestricted {
			dayOK = dayMatch || dowMatch // OR
		} else if dayRestricted {
			dayOK = dayMatch
		} else if dowRestricted {
			dayOK = dowMatch
		} else {
			dayOK = true // both wildcard
		}
		if !dayOK {
			t = addOneDay(t)
			continue
		}
		if !hour.values[t.Hour()] {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location()).Add(time.Hour)
			continue
		}
		if !minute.values[t.Minute()] {
			t = t.Add(time.Minute)
			continue
		}
		return t, nil
	}

	return time.Time{}, fmt.Errorf("no matching time found within 4 years for %q", expr)
}

// isWildcard returns true if the field is "*" (matches all values).
func isWildcard(field string) bool {
	return field == "*"
}

// parseField parses a single cron field into a set of matching integer values.
func parseField(field string, minVal, maxVal int) (cronField, error) {
	cf := cronField{
		values: make(map[int]bool),
		min:    minVal,
		max:    maxVal,
	}

	// Handle multiple comma-separated expressions
	parts := strings.Split(field, ",")
	for _, part := range parts {
		if err := parseFieldPart(part, minVal, maxVal, cf.values); err != nil {
			return cronField{}, err
		}
	}

	if len(cf.values) == 0 {
		return cronField{}, fmt.Errorf("field %q has no matching values", field)
	}

	return cf, nil
}

func parseFieldPart(part string, minVal, maxVal int, values map[int]bool) error {
	// Handle step (*/N, N-M/S)
	if strings.Contains(part, "/") {
		slashParts := strings.SplitN(part, "/", 2)
		if len(slashParts) != 2 {
			return fmt.Errorf("invalid step expression %q", part)
		}
		rangeStr := slashParts[0]
		stepStr := slashParts[1]

		step, err := strconv.Atoi(stepStr)
		if err != nil || step <= 0 {
			return fmt.Errorf("invalid step %q in %q", stepStr, part)
		}

		rangeStart, rangeEnd := minVal, maxVal
		if rangeStr == "*" {
			// */N — all values with step
		} else if strings.Contains(rangeStr, "-") {
			rParts := strings.SplitN(rangeStr, "-", 2)
			rangeStart, err = strconv.Atoi(rParts[0])
			if err != nil {
				return fmt.Errorf("invalid range start in %q", part)
			}
			rangeEnd, err = strconv.Atoi(rParts[1])
			if err != nil {
				return fmt.Errorf("invalid range end in %q", part)
			}
		} else {
			rangeStart, err = strconv.Atoi(rangeStr)
			if err != nil {
				return fmt.Errorf("invalid range in %q", part)
			}
			rangeEnd = maxVal
		}

		rangeStart = clamp(rangeStart, minVal, maxVal)
		rangeEnd = clamp(rangeEnd, minVal, maxVal)
		for v := rangeStart; v <= rangeEnd; v += step {
			values[v] = true
		}
		return nil
	}

	// Handle range (N-M)
	if strings.Contains(part, "-") {
		rParts := strings.SplitN(part, "-", 2)
		start, err := strconv.Atoi(rParts[0])
		if err != nil {
			return fmt.Errorf("invalid range start in %q", part)
		}
		end, err := strconv.Atoi(rParts[1])
		if err != nil {
			return fmt.Errorf("invalid range end in %q", part)
		}
		start = clamp(start, minVal, maxVal)
		end = clamp(end, minVal, maxVal)
		for v := start; v <= end; v++ {
			values[v] = true
		}
		return nil
	}

	// Wildcard *
	if part == "*" {
		for v := minVal; v <= maxVal; v++ {
			values[v] = true
		}
		return nil
	}

	// Single number
	v, err := strconv.Atoi(part)
	if err != nil {
		return fmt.Errorf("invalid value %q", part)
	}
	if v < minVal || v > maxVal {
		return fmt.Errorf("value %d out of range [%d-%d]", v, minVal, maxVal)
	}
	values[v] = true
	return nil
}

func clamp(v, minVal, maxVal int) int {
	if v < minVal {
		return minVal
	}
	if v > maxVal {
		return maxVal
	}
	return v
}

func firstOfNextMonth(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
}

func addOneDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
}

func splitFields(s string) []string {
	parts := strings.Fields(s)
	return parts
}
