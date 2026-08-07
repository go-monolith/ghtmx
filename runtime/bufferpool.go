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

// ReleaseAcquiredBuffer is the deferred half of a generated render
// function's buffer prologue, taking GetBuffer's two results and a
// pointer to the render's named error return:
//
//	ghtmxBuffer, ghtmxExisting := ghtmxruntime.GetBuffer(w)
//	defer ghtmxruntime.ReleaseAcquiredBuffer(ghtmxBuffer, ghtmxExisting, &err)
//
// existing reports that the writer was already a buffer, which means it
// belongs to an outer component: this call then does nothing, and that
// component releases it. Otherwise the buffer is drained and returned to
// the pool, and a flush failure becomes the render's error — but only
// when the render itself succeeded, so a render error is never
// overwritten.
//
// It is a plain function rather than a closure returned alongside the
// buffer so the generated defer keeps a statically-known callee: the
// compiler open-codes it onto the stack, where a deferred func value
// would cost an allocation on every component render (NFR-002 holds
// allocation counts exactly).
func ReleaseAcquiredBuffer(b *Buffer, existing bool, err *error) {
	if existing {
		return
	}
	bufErr := ReleaseBuffer(b)
	if err != nil && *err == nil {
		*err = bufErr
	}
}
