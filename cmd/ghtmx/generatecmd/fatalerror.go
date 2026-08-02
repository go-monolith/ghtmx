package generatecmd

type FatalError struct {
	Err error
}

func (e FatalError) Error() string {
	return e.Err.Error()
}

func (e FatalError) Unwrap() error {
	return e.Err
}

func (e FatalError) Is(target error) bool {
	_, ok := target.(FatalError)
	return ok
}

func (e FatalError) As(target any) bool {
	t, ok := target.(*FatalError)
	if !ok {
		return false
	}
	// The assignment is the point: errors.As reports success by this
	// return value, so without it a caller gets true and a zero
	// FatalError — its wrapped cause silently lost.
	*t = e
	return true
}
