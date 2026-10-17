// Package adapters defines the small contract shared by software adapters.
// Implementations report bounded runtime telemetry; the probe owns transport,
// scheduling, timestamps and the final observation envelope.
package adapters

import (
	"context"
	"errors"
	"os"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

type Command func(context.Context, string, ...string) ([]byte, error)

// Failure contains only a closed diagnostic code, never raw upstream text.
// Local distinguishes a monitor error from a failed target operation.
type Failure struct {
	Stage, Code string
	Local       bool
}

func (e *Failure) Error() string     { return e.Stage + ": " + e.Code }
func Local(stage, code string) error { return &Failure{Stage: stage, Code: code, Local: true} }
func Fail(stage string, err error) error {
	var se *os.SyscallError
	if errors.As(err, &se) && (se.Syscall == "bind" || se.Syscall == "socket" || se.Syscall == "setsockopt") {
		return Local("setup", "local_"+se.Syscall+"_failed")
	}
	if errors.Is(err, os.ErrPermission) {
		return Local("setup", "local_permission_denied")
	}
	return &Failure{Stage: stage, Code: secure.ErrorCode(err)}
}

// Withheld marks peer-controlled text withheld after sending credentials.
const Withheld = "withheld_secret_request"
