package spec

import (
	"sync"
	"testing"
)

// ★ Loads are not serialised. The configuration editor asks
// `/v2/settings/validate` while a save runs its own trial merge, and both walk
// every file — so anything the loader records in a shared place has to be safe
// to record from two goroutines at once. A duration that does not parse is
// recorded exactly there (decl.recordBadDuration), and `-race` is the only
// thing that would ever say so.
func TestConcurrentDiagnosticsAreRaceFree(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	// A duration with no unit: it parses leniently, and the loader records it.
	f.write(t, "tools/http.yaml", "timeout: nonsense\n")
	s := f.service(t)

	const workers = 8
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			d, err := s.Diagnostics("tools/http.yaml", []byte("timeout: also-nonsense\n"))
			if err != nil {
				t.Errorf("Diagnostics: %v", err)
				return
			}
			if !d.OK {
				t.Errorf("a lenient duration is a hint, not an error: %+v", d.Issues)
			}
		}()
	}
	wg.Wait()
}

// A save and a check share the same tree, so they share the same sink.
func TestConcurrentWriteAndCheckAreRaceFree(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "tools/http.yaml", "timeout: nonsense\n")
	s := f.service(t)

	var wg sync.WaitGroup
	wg.Add(4)
	go func() {
		defer wg.Done()
		_, _, _ = s.Write("tools/http.yaml", []byte("timeout: nonsense two\n"), "")
	}()
	for i := 0; i < 3; i++ {
		go func() {
			defer wg.Done()
			_, _ = s.Diagnostics("tools/http.yaml", []byte("timeout: nonsense three\n"))
		}()
	}
	wg.Wait()
}
