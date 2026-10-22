package sourceutil

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// ErrCredentialReflection never includes the credential or reflected data.
var ErrCredentialReflection = errors.New("credential appears in source report")

// GuardReport checks a bounded mapped report against an already resolved
// credential. Even legal timestamps and integers can reflect a credential.
// It handles JSON escaping without resolving or publishing any secret itself.
func GuardReport(r *model.ExternalReport, token string) error {
	if token == "" {
		return nil
	}
	encoded, err := json.Marshal(r)
	if err != nil {
		return err
	}
	encodedToken, err := json.Marshal(token)
	if err != nil {
		return err
	}
	if bytes.Contains(encoded, []byte(token)) || bytes.Contains(encoded, encodedToken[1:len(encodedToken)-1]) {
		return ErrCredentialReflection
	}
	return nil
}
