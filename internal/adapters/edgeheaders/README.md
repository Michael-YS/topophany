# Shared edge response labels

This package preserves the common short-label bounds, closed cache vocabulary
and last-hop parsing used by the edge header adapters. Keep provider recognition
and provider-specific interpretation in each platform's own directory.

Labels contain at most 24 ASCII letters, digits, hyphens, underscores or dots.
Cache labels are normalized and checked against the existing closed vocabulary;
the normalization order and truncated CloudFront label remain compatible with
the parser at commit `599c890`. Labels remain reports, not route verification.

Run `go test ./internal/adapters/edgeheaders`. The frozen parser and differential
fuzz test in `internal/probe` cover cross-provider behavior as well as these
shared rules. A new platform must not change existing label behavior implicitly.
