// Package logs multiplexes several container log streams onto one writer,
// prefixing every line with its source like `docker compose logs` does.
package logs

import (
	"bytes"
	"fmt"
	"io"
	"sync"
)

// palette holds the ANSI colors cycled across prefixes.
var palette = []string{"36", "33", "32", "35", "34", "96", "93", "92", "95", "94"}

// Mux serializes prefixed lines from many writers onto one destination.
type Mux struct {
	mu     sync.Mutex
	w      io.Writer
	width  int
	color  bool
	prefix bool
	next   int
}

// New returns a Mux for w. names are used to align prefixes; color enables
// ANSI colors; prefix=false writes raw lines (logs --no-log-prefix).
func New(w io.Writer, names []string, color, prefix bool) *Mux {
	width := 0
	for _, n := range names {
		width = max(width, len(n))
	}
	return &Mux{w: w, width: width, color: color, prefix: prefix}
}

// Writer returns a line-buffered writer tagged with name. Close flushes a
// trailing partial line.
func (m *Mux) Writer(name string) io.WriteCloser {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := ""
	if m.prefix {
		p = fmt.Sprintf("%-*s | ", m.width, name)
		if m.color {
			p = "\x1b[" + palette[m.next%len(palette)] + "m" + p + "\x1b[0m"
		}
	}
	m.next++
	return &line{mux: m, prefix: p}
}

type line struct {
	mux    *Mux
	prefix string
	buf    bytes.Buffer
}

func (l *line) Write(p []byte) (int, error) {
	l.buf.Write(p)
	for {
		i := bytes.IndexByte(l.buf.Bytes(), '\n')
		if i < 0 {
			return len(p), nil
		}
		if err := l.emit(l.buf.Next(i + 1)); err != nil {
			return len(p), err
		}
	}
}

func (l *line) Close() error {
	if l.buf.Len() == 0 {
		return nil
	}
	return l.emit(append(l.buf.Next(l.buf.Len()), '\n'))
}

func (l *line) emit(b []byte) error {
	l.mux.mu.Lock()
	defer l.mux.mu.Unlock()
	_, err := io.WriteString(l.mux.w, l.prefix+string(bytes.TrimRight(b, "\r\n"))+"\n")
	return err
}
