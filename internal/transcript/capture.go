package transcript

import (
	"bufio"
	"io"
	"strings"
)

// CaptureStream consumes a pane's pipe-pane byte stream from r until EOF,
// ANSI-strips each line (line-oriented flush per the contract), and appends
// every chunk that still carries text to w. Escape-only redraw lines strip to
// empty and are skipped — they carry no transcript content. Write errors are
// swallowed and the stream keeps draining: when this process sits behind a
// `tee` it must NEVER stop reading, or the tee'd pane log dies with it on
// SIGPIPE.
func CaptureStream(r io.Reader, w *Writer) {
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := br.ReadString('\n')
		if text := strings.TrimRight(Strip(strings.TrimRight(line, "\r\n")), " \t"); text != "" {
			_ = w.Append(text)
		}
		if err != nil {
			return
		}
	}
}
