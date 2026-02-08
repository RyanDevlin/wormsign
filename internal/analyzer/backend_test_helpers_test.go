package analyzer

import "fmt"

// errTestSecretRead is a sentinel error used by mock secret readers in tests.
var errTestSecretRead = fmt.Errorf("simulated secret read failure")
