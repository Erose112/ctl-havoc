package timeline

type ErrCode uint8

const (
	ErrNone ErrCode = iota
	ErrTimeout
	ErrConnRefused
	ErrConnReset
	ErrDNS
	ErrTLS
	ErrBadResponse
	ErrCanceled
	ErrUnknown
)

var errCodeNames = [...]string{
	ErrNone:        "none",
	ErrTimeout:     "timeout",
	ErrConnRefused: "conn_refused",
	ErrConnReset:   "conn_reset",
	ErrDNS:         "dns",
	ErrTLS:         "tls",
	ErrBadResponse: "bad_response",
	ErrCanceled:    "canceled",
	ErrUnknown:     "unknown",
}

func (c ErrCode) String() string {
	if int(c) < len(errCodeNames) {
		return errCodeNames[c]
	}
	return "invalid"
}