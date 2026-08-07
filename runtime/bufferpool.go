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

// AcquireBuffer returns the buffer to render into and the release func
// to defer. It is the whole buffer prologue of a generated render
// function, concentrated here rather than repeated in every one of
// them: the generated code's statements are the ones a consuming
// project's coverage tools count, and this is the largest block nobody
// wrote.
//
// The returned release takes a pointer to the caller's named error
// return so a flush failure is reported when the render itself
// succeeded, without overwriting a render error. It is a no-op when the
// writer was already a buffer — that buffer belongs to an outer
// component, which releases it.
//
//	buf, release := ghtmxruntime.AcquireBuffer(w)
//	defer release(&err)
func AcquireBuffer(w io.Writer) (buf *Buffer, release func(*error)) {
	b, existing := GetBuffer(w)
	if existing {
		return b, func(*error) {}
	}
	return b, func(err *error) {
		bufErr := ReleaseBuffer(b)
		if err != nil && *err == nil {
			*err = bufErr
		}
	}
}
