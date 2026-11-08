// Where this app talks to its own API.
//
// It cannot be derived from the build's base any more. The standalone build's
// base is `/nanoartifacts/`, but the remote (panel) build's base is the plugin
// directory — `/nanoartifacts/plugins/nanoartifacts/` — and deriving the API from
// it pointed every request at a path the static handler answers with index.html,
// so the panel showed an empty library.
//
// Both builds are served under the same prefix, so the API root is one constant.
// A deployment that mounts the app elsewhere changes this line.
export const API_BASE = '/nanoartifacts/api/v1'
