package runtime

import (
	"io"
	"sync"
)

var bufferPool = sync.Pool{
	New: func() any {
		return new(Buffer)
	},
}

// GetBuffer creates and returns a new buffer if the writer is not already a buffer,
// or returns the existing buffer if it is.
func GetBuffer(w io.Writer) (b *Buffer, existing bool) {
	if w == nil {
		return nil, false
	}
	b, ok := w.(*Buffer)
	if ok {
		return b, true
	}
	b = bufferPool.Get().(*Buffer)
	b.Reset(w)
	return b, false
}

// ReleaseBuffer drains the buffer into its underlying writer and returns
// it to the pool. It deliberately does not invoke the underlying
// http.Flusher: an implicit end-of-render flush would commit the response
// headers and force chunked transfer encoding — header effects the handler
// never requested (FR-034, FR-090). Streaming flushes stay explicit via
// the ghtmx.Flush() component.
func ReleaseBuffer(w io.Writer) (err error) {
	b, ok := w.(*Buffer)
	if !ok {
		return nil
	}
	err = b.b.Flush()
	bufferPool.Put(b)
	return err
}
