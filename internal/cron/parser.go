package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type cronField struct {
	values map[int]bool
	min    int
	max    int
}

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
	dow, err := parseField(fields[4], 0, 6)
	if err != nil {
		return time.Time{}, fmt.Errorf("day-of-week field: %w", err)
	}

	t := time.Date(from.Year(), from.Month(), from.Day(), from.Hour(), from.Minute(), 0, 0, from.Location()).Add(time.Minute)

	dayRestricted := !isWildcard(fields[2])
	dowRestricted := !isWildcard(fields[4])

	deadline := from.AddDate(4, 0, 0)
	maxIterations := 5 * 366 * 24 * 60
	for iter := 0; t.Before(deadline) && iter < maxIterations; iter++ {
		if !month.values[int(t.Month())] {
			t = firstOfNextMonth(t)
			continue
		}

		dayMatch := day.values[t.Day()]
		dowMatch := dow.values[int(t.Weekday())]
		var dayOK bool
		if dayRestricted && dowRestricted {
			dayOK = dayMatch || dowMatch
		} else if dayRestricted {
			dayOK = dayMatch
		} else if dowRestricted {
			dayOK = dowMatch
		} else {
			dayOK = true
		}
		if !dayOK {
			t = addOneDay(t)
			continue
		}
		if !hour.values[t.Hour()] {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
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

func isWildcard(field string) bool {
	return field == "*"
}

func parseField(field string, minVal, maxVal int) (cronField, error) {
	cf := cronField{
		values: make(map[int]bool),
		min:    minVal,
		max:    maxVal,
	}

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
		} else if strings.Contains(rangeStr, "-") {
			rParts := strings.SplitN(rangeStr, "-", 2)
			rangeStart, err = strconv.Atoi(rParts[0])
			if err != nil {
				return fmt.Errorf("invalid range start in %q", part)
			}
			endVal, rerr := strconv.Atoi(rParts[1])
			if rerr != nil {
				return fmt.Errorf("invalid range end in %q", part)
			}
			if endVal < rangeStart {
				return fmt.Errorf("reversed range %s in %q (start must be <= end)", rangeStr, part)
			}
			rangeEnd = endVal
		} else {
			rangeStart, err = strconv.Atoi(rangeStr)
			if err != nil {
				return fmt.Errorf("invalid range in %q", part)
			}
			rangeEnd = maxVal
		}

		if rangeStart < minVal || rangeStart > maxVal {
			return fmt.Errorf("value %d out of range [%d-%d] in %q", rangeStart, minVal, maxVal, part)
		}
		if rangeEnd < minVal || rangeEnd > maxVal {
			return fmt.Errorf("value %d out of range [%d-%d] in %q", rangeEnd, minVal, maxVal, part)
		}
		for v := rangeStart; v <= rangeEnd; v += step {
			values[v] = true
		}
		return nil
	}

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
		if start < minVal || start > maxVal {
			return fmt.Errorf("value %d out of range [%d-%d] in %q", start, minVal, maxVal, part)
		}
		if end < minVal || end > maxVal {
			return fmt.Errorf("value %d out of range [%d-%d] in %q", end, minVal, maxVal, part)
		}
		if end < start {
			return fmt.Errorf("reversed range %d-%d in %q (start must be <= end)", start, end, part)
		}
		for v := start; v <= end; v++ {
			values[v] = true
		}
		return nil
	}

	if part == "*" {
		for v := minVal; v <= maxVal; v++ {
			values[v] = true
		}
		return nil
	}

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
