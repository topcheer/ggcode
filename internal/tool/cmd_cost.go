package tool

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	costHintMinRuns      = 2
	costHintThreshold    = 2 * time.Minute
	costReportMinElapsed = 2 * time.Second
	costHistoryMax       = 16
)

type costStat struct {
	mu     sync.Mutex
	recent []time.Duration
}

func (c *costStat) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recent = append(c.recent, d)
	if len(c.recent) > costHistoryMax {
		c.recent = c.recent[len(c.recent)-costHistoryMax:]
	}
}

func (c *costStat) snapshot() (n int, avg time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.recent) == 0 {
		return 0, 0
	}
	var total time.Duration
	for _, d := range c.recent {
		total += d
	}
	return len(c.recent), total / time.Duration(len(c.recent))
}

var commandCosts sync.Map

func normalizeCostKey(cmd string) string {
	cmd = stripTrailingComment(strings.TrimSpace(cmd))

	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return ""
	}
	cmd = strings.Join(fields, " ")
	if len(cmd) > 160 {
		cmd = cmd[:160]
	}
	return cmd
}

func stripTrailingComment(cmd string) string {
	var quote rune
	for i, r := range cmd {
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case '#':
			return cmd[:i]
		case '/':
			if i+1 < len(cmd) && cmd[i+1] == '/' {
				prev := byte(' ')
				if i > 0 {
					prev = cmd[i-1]
				}
				if prev == ' ' || prev == '\t' {
					return cmd[:i]
				}
			}
		}
	}
	return cmd
}

func recordCommandCost(cmd string, d time.Duration) {
	if cmd == "" || d <= 0 {
		return
	}
	key := normalizeCostKey(cmd)
	v, _ := commandCosts.LoadOrStore(key, &costStat{})
	v.(*costStat).add(d)
}

func commandCostHint(cmd string) string {
	key := normalizeCostKey(cmd)
	v, ok := commandCosts.Load(key)
	if !ok {
		return ""
	}
	n, avg := v.(*costStat).snapshot()
	if n < costHintMinRuns || avg < costHintThreshold {
		return ""
	}
	return fmt.Sprintf("[cost hint] this command averaged ~%s over %d recent runs - consider a narrower scope (package filter / -run pattern) for faster iteration",
		avg.Round(time.Second), n)
}

func formatCommandCost(cmd string, elapsed time.Duration) string {
	hint := commandCostHint(cmd)
	if hint == "" && elapsed.Round(time.Second) < costReportMinElapsed {
		return ""
	}
	line := fmt.Sprintf("[cost] took %s", elapsed.Round(time.Second))
	if hint != "" {
		line += "\n" + hint
	}
	return line
}
