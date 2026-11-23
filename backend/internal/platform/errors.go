package platform

// Error is the API error returned to clients. It deliberately mirrors the
// existing frontend contract of `{error, code}` plus an HTTP status.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func NewError(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

func asError(err error, fallbackStatus int) *Error {
	if err == nil {
		return nil
	}
	if apiErr, ok := err.(*Error); ok {
		return apiErr
	}
	return NewError(fallbackStatus, "INTERNAL", err.Error())
}

func invalid(message string) *Error {
	return NewError(400, "INVALID_INPUT", message)
}

func unauthorized(message string) *Error {
	return NewError(401, "UNAUTHORIZED", message)
}

func forbidden(message string) *Error {
	return NewError(403, "FORBIDDEN", message)
}

func notFound(message string) *Error {
	return NewError(404, "NOT_FOUND", message)
}
