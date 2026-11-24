// Package browsersdk exposes the SDK's native ES modules to the host relay.
package browsersdk

import _ "embed"

//go:embed src/index.js
var Index []byte

//go:embed src/policy.js
var Policy []byte
