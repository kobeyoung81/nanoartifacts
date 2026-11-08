// Package migrations embeds the MongoDB manifests this service applies.
//
// The manifests live next to this file so a compiled binary carries them: a
// container image needs no extra mount and `nanoartifacts migrate up` works in
// any environment the binary runs in.
package migrations

import "embed"

// FS holds every manifest, ordered by filename.
//
//go:embed *.json
var FS embed.FS
