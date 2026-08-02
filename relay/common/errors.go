package common

import "errors"

// ErrLegacyAdaptorNotImplemented marks adaptor methods that historically
// panicked with "implement me". Adaptors return it so unsupported code stays
// explicit and testable; the relay boundary preserves the legacy panic
// contract for callers that reach that impossible routing branch.
var ErrLegacyAdaptorNotImplemented = errors.New("implement me")
