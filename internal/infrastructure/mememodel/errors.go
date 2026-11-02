package mememodel

import (
	"errors"
	"fmt"
	"regexp"

	arkmodel "github.com/volcengine/volcengine-go-sdk/service/arkruntime/model"
)

// TransportError deliberately retains no original provider message, request ID or wrapped raw error.
type TransportError struct {
	HTTPStatus int
	Code       string
	Param      string
}

func (e *TransportError) Error() string {
	return fmt.Sprintf("%s (status=%d code=%s param=%s)", ErrTransport, e.HTTPStatus, e.Code, e.Param)
}
func (e *TransportError) Unwrap() error { return ErrTransport }

var safeProviderCode = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.]{0,95}$`)
var safeProviderParam = regexp.MustCompile(`^[a-z_][a-z0-9_.\[\]]{0,95}$`)

func safeTransportError(err error) error {
	safe := &TransportError{Code: "unknown"}
	var api *arkmodel.APIError
	if errors.As(err, &api) {
		safe.HTTPStatus = safeStatus(api.HTTPStatusCode)
		if safeProviderCode.MatchString(api.Code) {
			safe.Code = api.Code
		}
		if api.Param != nil && safeProviderParam.MatchString(*api.Param) {
			safe.Param = *api.Param
		}
		return safe
	}
	var request *arkmodel.RequestError
	if errors.As(err, &request) {
		safe.HTTPStatus = safeStatus(request.HTTPStatusCode)
		safe.Code = "request_error"
	}
	return safe
}
func safeStatus(status int) int {
	if status >= 100 && status <= 599 {
		return status
	}
	return 0
}

// SafeTransportDetails exposes only bounded protocol metadata, never err.Error() from an upstream provider.
func SafeTransportDetails(err error) (status int, code, param string) {
	var safe *TransportError
	if errors.As(err, &safe) {
		return safe.HTTPStatus, safe.Code, safe.Param
	}
	if err == nil {
		return 0, "", ""
	}
	if errors.Is(err, ErrTransport) {
		return 0, "unknown", ""
	}
	return 0, "non_transport", ""
}
