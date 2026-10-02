package pool

import "strings"

// TaskError associates an error returned by a task with the index of that
// task. The index is zero-based and matches the order in which tasks were
// submitted to the pool.
type TaskError struct {
	// Index is the zero-based index of the task that returned Err.
	Index int

	// Err is the error returned by the task.
	Err error
}

func (e TaskError) Error() string {
	return e.Err.Error()
}

// TaskIndex returns the zero-based index of the task that returned the error.
func (e TaskError) TaskIndex() int {
	return e.Index
}

func (e TaskError) Unwrap() error {
	return e.Err
}

// TaskErrors is an ordered collection of TaskError values returned by a pool
// configured with WithOrderedErrors.
type TaskErrors []TaskError

func (errs TaskErrors) Error() string {
	var message strings.Builder
	for i, err := range errs {
		if i > 0 {
			message.WriteByte('\n')
		}
		message.WriteString(err.Error())
	}
	return message.String()
}

func (errs TaskErrors) Unwrap() []error {
	unwrapped := make([]error, len(errs))
	for i, err := range errs {
		unwrapped[i] = err
	}
	return unwrapped
}
