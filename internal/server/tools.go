package server

// toolDefinitions is the contract the model sees. Descriptions say what the
// tool does and what it returns; schemas are closed to what the server accepts,
// so a model cannot invent a "user_id" argument for publish and have it silently
// ignored.
func toolDefinitions() []map[string]any {
	return []map[string]any{
		{
			"name": "artifact_publish",
			"description": "Publish a self-contained HTML document to the platform library. " +
				"Write the document to a file in your workspace and pass its path as html_file: the runtime uploads the bytes, so the document never travels through the tool arguments. " +
				"Without artifact_id this creates a new work; with artifact_id it appends a version " +
				"(or returns the existing version when the content is unchanged). " +
				"The HTML must be a complete document (doctype or <html>), single file, at most 16 MiB.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"html_file": map[string]any{
						"type":        "string",
						"format":      "uri",
						"description": "Path to the HTML file inside your workspace, e.g. artifacts/report.html. Pass the path itself; the runtime uploads it for you.",
						"x-mcp-file": map[string]any{
							"transferModes":  []string{"upload"},
							"uploadEndpoint": "/mcp/blobs",
							"accept":         []string{"text/html"},
							"maxSize":        16 << 20,
						},
					},
					"html": map[string]any{
						"type":        "string",
						"description": "Deprecated: the complete HTML document inline. Prefer html_file — it keeps the document out of the conversation.",
					},
					"title":       map[string]any{"type": "string", "description": "Library title; required when creating."},
					"kind":        map[string]any{"type": "string", "enum": []string{"doc", "ppt", "ui", "pro", "html"}, "description": "Container kind."},
					"note":        map[string]any{"type": "string", "description": "What changed in this version."},
					"artifact_id": map[string]any{"type": "string", "description": "Existing work to add a version to."},
					"space_id":    map[string]any{"type": "string", "description": "Space to publish into; defaults to the caller's personal space."},
					"node_id":     map[string]any{"type": "string", "description": "Node to publish into (appends a version if it already holds a work)."},
					"parent_id":   map[string]any{"type": "string", "description": "Folder to create the new node in; defaults to the space root."},
				},
				"additionalProperties": false,
			},
		},
		{
			"name":        "artifact_list",
			"description": "List works visible to the current user.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scope": map[string]any{"type": "string", "enum": []string{"owned", "shared"}, "description": "owned (default) or shared with me."},
				},
				"additionalProperties": false,
			},
		},
		{
			"name":        "space_list",
			"description": "List the spaces this user can open (personal first), with the caller's role.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		},
		{
			"name":        "tree_get",
			"description": "Read a space's tree (folders, documents, apps) so you can choose where new content belongs.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"space_id": map[string]any{"type": "string"},
				},
				"required":             []string{"space_id"},
				"additionalProperties": false,
			},
		},
		{
			"name": "artifact_get",
			"description": "Read one work's metadata and version history. " +
				"To read the document itself, pass download_to: the runtime saves the body to that workspace path and returns where it went, instead of loading a large document into the conversation. " +
				"Set include_html only for a small body you need inline; seq picks a version.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"artifact_id": map[string]any{"type": "string"},
					"seq":         map[string]any{"type": "integer", "description": "Version to read; defaults to the current one."},
					"download_to": map[string]any{
						"type":        "string",
						"format":      "uri",
						"description": "Workspace path to save the body to, e.g. artifacts/report.html. The runtime downloads the bytes.",
						"x-mcp-file": map[string]any{
							"transferModes": []string{"download"},
							"maxSize":       16 << 20,
						},
					},
					"include_html": map[string]any{
						"type":        "boolean",
						"description": "Return the HTML body as well (capped at 8 MiB). Off by default: most callers only need the metadata.",
					},
				},
				"required":             []string{"artifact_id"},
				"additionalProperties": false,
			},
		},
		{
			"name":        "artifact_rename",
			"description": "Rename a work. Pass node_id (the tree row) or artifact_id; the tree title and the document title are both updated.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"artifact_id": map[string]any{"type": "string"},
					"node_id":     map[string]any{"type": "string", "description": "The tree row; preferred when renaming from the tree."},
					"title":       map[string]any{"type": "string"},
				},
				"required":             []string{"title"},
				"additionalProperties": false,
			},
		},
		{
			"name":        "artifact_grant",
			"description": "Give another platform user view access to a work you own.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"artifact_id": map[string]any{"type": "string"},
					"email":       map[string]any{"type": "string", "description": "Account email; alternative to user_id."},
					"user_id":     map[string]any{"type": "string"},
				},
				"required":             []string{"artifact_id"},
				"additionalProperties": false,
			},
		},
		{
			"name":        "artifact_delete",
			"description": "Remove a work you own from the library. Other users lose access immediately.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"artifact_id": map[string]any{"type": "string"},
				},
				"required":             []string{"artifact_id"},
				"additionalProperties": false,
			},
		},
	}
}
