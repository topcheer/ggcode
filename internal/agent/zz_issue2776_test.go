package agent

import "testing"

// #2776 verification scenarios.
func TestIssue2776Scenarios(t *testing.T) {
	// Scenario 1: producer loop close+return + final close after loop
	src1 := `package p
func produce(items []int, ch chan int) {
	for _, v := range items {
		if v < 0 {
			close(ch)
			return
		}
		ch <- v
	}
	close(ch)
}
`
	if w := checkChannelSafety("a.go", "", src1); len(w) != 0 {
		t.Errorf("scenario1 (close+return producer): expected 0 warnings, got %v", w)
	}

	// Scenario 2: mutually exclusive error branch
	src2 := `package p
func send(ch chan int) error {
	if err := prep(); err != nil {
		close(ch)
		return err
	}
	ch <- 1
	close(ch)
	return nil
}
func prep() error { return nil }
`
	if w := checkChannelSafety("a.go", "", src2); len(w) != 0 {
		t.Errorf("scenario2 (exclusive err-branch close): expected 0 warnings, got %v", w)
	}

	// Scenario 3: close+break loop exit
	src3 := `package p
func pump(in, out chan int) {
	for v := range in {
		if v < 0 {
			close(out)
			break
		}
		out <- v
	}
}
`
	if w := checkChannelSafety("a.go", "", src3); len(w) != 0 {
		t.Errorf("scenario3 (close+break exit): expected 0 warnings, got %v", w)
	}

	// True positives must still fire.
	tp := `package p
func bad(ch chan int) {
	close(ch)
	close(ch)
	ch <- 1
}
`
	if w := checkChannelSafety("a.go", "", tp); len(w) == 0 {
		t.Errorf("true positive: expected warnings, got none")
	}
}
