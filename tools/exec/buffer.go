package exec

import (
	"sync"
	"time"
	"unicode/utf8"
)

// boundedBuffer keeps a stable prefix and a bounded suffix of a process's
// output. Everything in between is dropped and counted, so a chatty process
// cannot exhaust memory while the most useful bytes — the start and the end —
// remain readable.
//
// Offsets are absolute byte positions in the stream. They are stable for the
// lifetime of the buffer: dropping bytes from the middle never rewrites the
// offset of the bytes that remain. A cursor that points into a dropped region
// is reset to the earliest retained byte.
type boundedBuffer struct {
	mu   sync.Mutex
	cond chan struct{}

	head      []byte
	tail      []byte
	headLimit int
	tailLimit int
	total     int64
	closed    bool
}

func newBoundedBuffer(totalBytes int) *boundedBuffer {
	if totalBytes <= 0 {
		totalBytes = defaultProcessBufferBytes
	}
	if totalBytes < 2 {
		totalBytes = 2
	}
	headLimit := totalBytes / 4
	if headLimit < 1 {
		headLimit = 1
	}
	tailLimit := totalBytes - headLimit
	if tailLimit < 1 {
		tailLimit = 1
		headLimit = totalBytes - 1
	}
	return &boundedBuffer{
		cond:      make(chan struct{}),
		headLimit: headLimit,
		tailLimit: tailLimit,
	}
}

// Write appends p to the buffer. It never blocks and always reports len(p)
// once accepted; writes after Close are dropped.
func (b *boundedBuffer) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	chunk := append([]byte(nil), p...)

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return len(p), nil
	}

	if remaining := b.headLimit - len(b.head); remaining > 0 {
		take := min(remaining, len(chunk))
		b.head = append(b.head, chunk[:take]...)
		chunk = chunk[take:]
	}
	if len(chunk) > 0 {
		b.tail = append(b.tail, chunk...)
		if drop := len(b.tail) - b.tailLimit; drop > 0 {
			// Copy the retained suffix when the backing array grew far past
			// the limit; otherwise append would keep reusing a stale prefix.
			if cap(b.tail) > b.tailLimit*2 {
				b.tail = append([]byte(nil), b.tail[drop:]...)
			} else {
				b.tail = b.tail[drop:]
			}
		}
	}
	b.total += int64(len(p))
	b.notifyLocked()
	return len(p), nil
}

// Close stops notifications. Retained bytes stay readable.
func (b *boundedBuffer) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	b.notifyLocked()
}

func (b *boundedBuffer) notifyLocked() {
	close(b.cond)
	b.cond = make(chan struct{})
}

// WaitForData blocks until the buffer has more than cursor bytes, closes, the
// timeout expires, or ctx is done. It checks the cursor while holding the
// buffer lock so a write between Read and Wait cannot be missed.
func (b *boundedBuffer) WaitForData(ctxDone <-chan struct{}, cursor int64, timeout time.Duration) bool {
	b.mu.Lock()
	if b.closed || b.total > cursor {
		b.mu.Unlock()
		return true
	}
	wait := b.cond
	b.mu.Unlock()

	if timeout <= 0 {
		select {
		case <-wait:
			return true
		case <-ctxDone:
			return false
		}
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-wait:
		return true
	case <-timer.C:
		return false
	case <-ctxDone:
		return false
	}
}

// Total returns the number of bytes ever written.
func (b *boundedBuffer) Total() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.total
}

// Omitted returns the number of bytes dropped from the middle.
func (b *boundedBuffer) Omitted() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.omittedLocked()
}

func (b *boundedBuffer) omittedLocked() int64 {
	retained := int64(len(b.head) + len(b.tail))
	if b.total <= retained {
		return 0
	}
	return b.total - retained
}

// Read returns up to maxBytes starting at cursor. When the cursor points into
// a dropped region, the read resumes at the earliest retained byte and reports
// how many bytes were skipped.
func (b *boundedBuffer) Read(cursor int64, maxBytes int) readChunk {
	b.mu.Lock()
	defer b.mu.Unlock()

	if maxBytes <= 0 {
		maxBytes = defaultProcessReadBytes
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor > b.total {
		return readChunk{next: b.total}
	}

	headLen := int64(len(b.head))
	tailStart := b.total - int64(len(b.tail))
	reset := false
	var omitted int64
	remaining := maxBytes
	out := make([]byte, 0, min(maxBytes, len(b.head)+len(b.tail)))

	if cursor < headLen && remaining > 0 {
		take := min(int64(remaining), headLen-cursor)
		out = append(out, b.head[cursor:cursor+take]...)
		cursor += take
		remaining -= int(take)
	}
	if remaining > 0 && cursor < tailStart {
		reset = true
		omitted = tailStart - cursor
		cursor = tailStart
	}
	if remaining > 0 && cursor >= tailStart && cursor < b.total {
		offset := cursor - tailStart
		if offset < 0 {
			offset = 0
		}
		take := min(int64(remaining), int64(len(b.tail))-offset)
		out = append(out, b.tail[offset:offset+take]...)
		cursor += take
	}
	return readChunk{
		data:    out,
		next:    cursor,
		omitted: omitted,
		reset:   reset,
		hasMore: cursor < b.total,
	}
}

// Snapshot returns a bounded view of the retained output. When the buffer
// dropped bytes, the view is head + marker + tail.
func (b *boundedBuffer) Snapshot(maxBytes int) snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()

	if maxBytes <= 0 {
		maxBytes = defaultProcessReadBytes
	}
	total := b.total
	if total == 0 {
		return snapshot{}
	}
	retained := int64(len(b.head) + len(b.tail))
	if total <= int64(maxBytes) && total <= retained {
		data := make([]byte, 0, len(b.head)+len(b.tail))
		data = append(data, b.head...)
		data = append(data, b.tail...)
		return snapshot{data: data, next: total}
	}

	headTake := min(len(b.head), maxBytes/4)
	tailTake := min(len(b.tail), maxBytes-headTake)
	data := make([]byte, 0, headTake+tailTake+64)
	data = append(data, b.head[:headTake]...)
	omitted := total - int64(headTake+tailTake)
	if omitted > 0 {
		data = append(data, []byte("\n[... omitted "+formatBytes(omitted)+" ...]\n")...)
	}
	data = append(data, b.tail[len(b.tail)-tailTake:]...)
	return snapshot{
		data:      data,
		next:      total,
		omitted:   omitted,
		truncated: omitted > 0,
	}
}

// TailLines returns the last lines of the retained output, capped at maxBytes.
func (b *boundedBuffer) TailLines(lines, maxBytes int) snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()

	if lines <= 0 {
		lines = defaultProcessTailLines
	}
	if maxBytes <= 0 {
		maxBytes = defaultProcessReadBytes
	}
	data := make([]byte, 0, len(b.head)+len(b.tail)+64)
	data = append(data, b.head...)
	if omitted := b.omittedLocked(); omitted > 0 {
		data = append(data, []byte("\n[... omitted "+formatBytes(omitted)+" ...]\n")...)
	}
	data = append(data, b.tail...)

	data = tailLines(data, lines)
	if len(data) > maxBytes {
		data = data[len(data)-maxBytes:]
		data = trimToRuneStart(data)
	}
	omitted := b.total - int64(len(data))
	if omitted < 0 {
		omitted = 0
	}
	return snapshot{
		data:      data,
		next:      b.total,
		omitted:   omitted,
		truncated: omitted > 0,
	}
}

type readChunk struct {
	data    []byte
	next    int64
	omitted int64
	reset   bool
	hasMore bool
}

type snapshot struct {
	data      []byte
	next      int64
	omitted   int64
	truncated bool
}

func (s snapshot) String() string {
	return string(s.data)
}

// tailLines returns the last lines of data. lines<=0 returns all data.
func tailLines(data []byte, lines int) []byte {
	if lines <= 0 || len(data) == 0 {
		return data
	}
	end := len(data)
	if end > 0 && data[end-1] == '\n' {
		end--
	}
	start := end
	count := 0
	for i := end - 1; i >= 0; i-- {
		if data[i] != '\n' {
			continue
		}
		if count == lines {
			break
		}
		count++
		start = i + 1
		if count == lines {
			break
		}
	}
	if count < lines {
		start = 0
	}
	return data[start:]
}

// trimToRuneStart drops leading bytes until the slice starts at a rune
// boundary, so a byte cap never splits a UTF-8 character.
func trimToRuneStart(data []byte) []byte {
	for len(data) > 0 && !utf8.RuneStart(data[0]) {
		data = data[1:]
	}
	return data
}

func formatBytes(n int64) string {
	switch {
	case n < 1024:
		return itoa(n) + " B"
	case n < 1024*1024:
		return itoa(n/1024) + " KiB"
	default:
		return itoa(n/(1024*1024)) + " MiB"
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	negative := n < 0
	if negative {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
