// Package browsersdk exposes the SDK's native ES modules to the host relay.
package browsersdk

import "embed"

//go:embed src/index.js src/policy.js
var Files embed.FS
