// Package contractfixture is intentionally NOT built by the Go toolchain
// (testdata directories are ignored). It exists to verify the contract scanner
// rejects external driver sources that import internal packages.
package contractfixture

import (
	_ "github.com/DeliciousBuding/cloud-path/internal/model"
)
