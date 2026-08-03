package agent

import (
	"strings"
	"testing"
)

func TestCheckGoroutineLeak_FireAndForget(t *testing.T) {
	src := `package main

func process(jobs []int) {
	for _, j := range jobs {
		go handleJob(j)
	}
}

func handleJob(j int) {}
`
	warnings := checkGoroutineLeak("test.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected goroutine leak warning for fire-and-forget goroutine")
	}
	if !strings.Contains(warnings[0], "goroutine leak") {
		t.Errorf("unexpected warning: %s", warnings[0])
	}
}

func TestCheckGoroutineLeak_WithWaitGroup(t *testing.T) {
	src := `package main

import "sync"

func process(jobs []int) {
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func(job int) {
			defer wg.Done()
			handleJob(job)
		}(j)
	}
	wg.Wait()
}

func handleJob(j int) {}
`
	warnings := checkGoroutineLeak("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings with WaitGroup, got: %v", warnings)
	}
}

func TestCheckGoroutineLeak_WithContextCancel(t *testing.T) {
	src := `package main

import "context"

func worker(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
	}()
}
`
	warnings := checkGoroutineLeak("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings with context cancellation, got: %v", warnings)
	}
}

func TestCheckGoroutineLeak_WithErrgroup(t *testing.T) {
	src := `package main

import "golang.org/x/sync/errgroup"

func worker() {
	eg, ctx := errgroup.WithContext(context.Background())
	eg.Go(func() error {
		return nil
	})
	eg.Wait()
}
`
	warnings := checkGoroutineLeak("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings with errgroup, got: %v", warnings)
	}
}

func TestCheckGoroutineLeak_WithChannelSignal(t *testing.T) {
	src := `package main

func worker() {
	stop := make(chan struct{})
	go func() {
		<-stop
	}()
	close(stop)
}
`
	warnings := checkGoroutineLeak("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings with channel signal, got: %v", warnings)
	}
}

func TestCheckGoroutineLeak_DeltaAware(t *testing.T) {
	oldSrc := `package main

func worker() {
	go leak()
}

func leak() {}
`
	// Same content should not trigger since no new leaks were introduced.
	warnings := checkGoroutineLeak("test.go", oldSrc, oldSrc)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for unchanged content, got: %v", warnings)
	}

	// Adding a new goroutine leak should trigger.
	newSrc := oldSrc + `
func worker2() {
	go leak2()
}

func leak2() {}
`
	warnings = checkGoroutineLeak("test.go", oldSrc, newSrc)
	if len(warnings) == 0 {
		t.Fatal("expected warning for newly introduced goroutine leak")
	}
}

func TestCheckGoroutineLeak_SkipMain(t *testing.T) {
	src := `package main

import (
	"fmt"
	"time"
)

func main() {
	go func() {
		time.Sleep(time.Second)
		fmt.Println("done")
	}()
	time.Sleep(2 * time.Second)
}
`
	warnings := checkGoroutineLeak("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings in main(), got: %v", warnings)
	}
}

func TestCheckGoroutineLeak_AnonymousFunc(t *testing.T) {
	src := `package main

func worker() {
	go func() {
		panic("oops")
	}()
}
`
	warnings := checkGoroutineLeak("test.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected goroutine leak warning for anonymous func")
	}
}

func TestCheckGoroutineLeak_NonGoFile(t *testing.T) {
	warnings := checkGoroutineLeak("test.py", "", "print('hello')")
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for non-Go file")
	}
}

func TestCheckGoroutineLeak_EmptyContent(t *testing.T) {
	warnings := checkGoroutineLeak("test.go", "", "")
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for empty content")
	}
}

func TestCheckGoroutineLeak_StopChannelSend(t *testing.T) {
	src := `package main

func worker() {
	stop := make(chan struct{})
	go func() {
		<-stop
	}()
	stop <- struct{}{}
}
`
	warnings := checkGoroutineLeak("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings with stop channel send, got: %v", warnings)
	}
}
