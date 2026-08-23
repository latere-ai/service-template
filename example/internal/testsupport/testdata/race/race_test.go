package race

import (
	"sync"
	"testing"
)

// TestUnsynchronizedWrite writes one variable from two goroutines with no
// synchronization. It is a fixture: the tier that runs it expects it to fail
// under the race detector and to pass without it, which is what proves the
// detector is switched on.
func TestUnsynchronizedWrite(t *testing.T) {
	var wg sync.WaitGroup
	total := 0
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 1000 {
				total += i
			}
		}()
	}
	wg.Wait()
	if total < 0 {
		t.Fatalf("total = %d", total)
	}
}
