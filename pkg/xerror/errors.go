package xerror

import (
	"errors"
	"strings"
)

type toolFeedbackError struct {
	err      error
	feedback string
}

func (e *toolFeedbackError) Error() string {
	return e.err.Error()
}

func (e *toolFeedbackError) Unwrap() error {
	return e.err
}

func (e *toolFeedbackError) ToolFeedback() string {
	return e.feedback
}

// WithToolFeedback wraps err with trimmed feedback explicitly designated for
// tool-facing use. It returns err unchanged when err is nil or feedback is blank.
func WithToolFeedback(err error, feedback string) error {
	feedback = strings.TrimSpace(feedback)
	if err == nil || feedback == "" {
		return err
	}

	return &toolFeedbackError{err: err, feedback: feedback}
}

// ToolFeedback returns trusted, non-empty feedback from a WithToolFeedback
// wrapper in err's chain. It returns ("", false) when no such feedback exists.
func ToolFeedback(err error) (string, bool) {
	var wrapped *toolFeedbackError
	if !errors.As(err, &wrapped) {
		return "", false
	}

	feedback := strings.TrimSpace(wrapped.ToolFeedback())
	if feedback == "" {
		return "", false
	}

	return feedback, true
}

var (
	ErrStageSkip         = errors.New("Stage Skip")
	ErrStageError        = errors.New("Stage Error")
	ErrStageWarn         = errors.New("Stage Warn")
	ErrArgsIncompelete   = errors.New("ArgsIncompeleteError")
	ErrCommandNotFound   = errors.New("CommandNotFoundError")
	ErrCommandIncomplete = errors.New("CommandIncompleteError")
)
