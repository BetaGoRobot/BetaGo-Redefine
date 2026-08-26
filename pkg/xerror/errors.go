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

type toolFeedbackProvider interface {
	ToolFeedback() string
}

func WithToolFeedback(err error, feedback string) error {
	feedback = strings.TrimSpace(feedback)
	if err == nil || feedback == "" {
		return err
	}

	return &toolFeedbackError{err: err, feedback: feedback}
}

func ToolFeedback(err error) (string, bool) {
	var provider toolFeedbackProvider
	if !errors.As(err, &provider) {
		return "", false
	}

	feedback := strings.TrimSpace(provider.ToolFeedback())
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
