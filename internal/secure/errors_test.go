package secure

import (
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"testing"
)

func TestErrorCodePreservesWrappedDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{nil, ""},
		{x509.UnknownAuthorityError{}, "tls_unknown_authority"},
		{x509.HostnameError{}, "tls_name_mismatch"},
		{x509.CertificateInvalidError{}, "tls_certificate_invalid"},
		{os.ErrDeadlineExceeded, "timeout"},
		{os.ErrPermission, "permission_denied"},
		{errors.New("connection refused"), "network_error"},
	} {
		if got := ErrorCode(tc.err); got != tc.want {
			t.Fatalf("%T: %q != %q", tc.err, got, tc.want)
		}
		if tc.err != nil {
			if got := ErrorCode(fmt.Errorf("transport: %w", tc.err)); got != tc.want {
				t.Fatalf("wrapped %T: %q", tc.err, got)
			}
		}
	}
}
