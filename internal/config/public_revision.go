package config

import "github.com/WhitenWhiten/topophany/internal/model"

// PublicRevision identifies only the redacted topology. Never expose Revision
// to viewers: its digest permits offline guesses of low-entropy hidden fields.
// Executors and storage must continue using Revision so changing credentials or
// other private execution settings still invalidates old observations.
func PublicRevision(c model.Config) string {
	return Revision(Public(c))
}
