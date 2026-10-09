package logs

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestPrefixAndBuffering(t *testing.T) {
	var out bytes.Buffer
	m := New(&out, []string{"web", "database"}, false, true)
	w := m.Writer("web")
	fmt.Fprint(w, "hel")
	fmt.Fprint(w, "lo\r\nwor")
	w.Close()
	want := "web      | hello\nweb      | wor\n"
	if out.String() != want {
		t.Fatalf("got %q want %q", out.String(), want)
	}
}

func TestNoPrefixAndColor(t *testing.T) {
	var out bytes.Buffer
	New(&out, nil, false, false).Writer("x").Write([]byte("raw\n"))
	if out.String() != "raw\n" {
		t.Fatalf("got %q", out.String())
	}
	out.Reset()
	New(&out, []string{"a"}, true, true).Writer("a").Write([]byte("c\n"))
	if !strings.HasPrefix(out.String(), "\x1b[36ma | \x1b[0m") {
		t.Fatalf("got %q", out.String())
	}
}

func TestConcurrentLinesStayWhole(t *testing.T) {
	var out bytes.Buffer
	m := New(&out, []string{"a", "b"}, false, true)
	var wg sync.WaitGroup
	for _, n := range []string{"a", "b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := m.Writer(n)
			for i := 0; i < 200; i++ {
				fmt.Fprintf(w, "line %d\n", i)
			}
		}()
	}
	wg.Wait()
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if !strings.HasPrefix(l, "a | line ") && !strings.HasPrefix(l, "b | line ") {
			t.Fatalf("interleaved line %q", l)
		}
	}
}
