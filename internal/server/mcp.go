package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"gitee.com/kobeyoung81/nanoartifacts/internal/identity"
	"gitee.com/kobeyoung81/nanoartifacts/internal/store"
)

// The MCP surface is what agents call. It speaks JSON-RPC 2.0 over a single
// HTTP endpoint (the "streamable HTTP" shape): initialize, tools/list and
// tools/call, with notifications acknowledged and ignored.
//
// Identity comes from X-Nanowork-Agent-ID, never from tool arguments: an agent
// must not be able to publish under someone else's name by editing a parameter.

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	var req rpcRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeRPCError(w, nil, -32700, "parse error")
		return
	}

	switch req.Method {
	case "initialize":
		writeRPCResult(w, req.ID, map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "nanoartifacts", "version": s.Version},
		})
	case "notifications/initialized", "notifications/cancelled":
		w.WriteHeader(http.StatusAccepted)
	case "ping":
		writeRPCResult(w, req.ID, map[string]any{})
	case "tools/list":
		writeRPCResult(w, req.ID, map[string]any{"tools": toolDefinitions()})
	case "tools/call":
		// Only this method acts as an agent, so only this method needs one.
		agentID := r.Header.Get("X-Nanowork-Agent-ID")
		if strings.TrimSpace(agentID) == "" {
			writeRPCError(w, req.ID, -32602, "X-Nanowork-Agent-ID is required for tools/call")
			return
		}
		owner, err := s.Identity.OwnerOfAgent(r.Context(), agentID)
		if err != nil {
			if errors.Is(err, identity.ErrUnknownAgent) {
				// A stale container or a stale tab: the caller's mistake, and the
				// answer says so.
				slog.Warn("mcp identity lookup: no such agent", "agent", agentID, "error", err)
				writeRPCError(w, req.ID, -32000, "unknown agent")
				return
			}
			// The owner of the agent rows could not answer. Reporting that as
			// "unknown agent" is what kept a renamed surface invisible: the
			// platform is unwell, not the caller.
			slog.Error("mcp identity lookup failed", "agent", agentID, "error", err)
			writeRPCError(w, req.ID, -32000, "agent directory unavailable")
			return
		}
		s.callTool(w, r.Context(), owner, req)
	default:
		writeRPCError(w, req.ID, -32601, "method not found: "+req.Method)
	}
}

// maxInlineBody caps what artifact_get will put in a JSON-RPC response. Beyond
// this a caller should download the file: the body is already in the blob store
// and JSON-RPC is not a file transfer.
const maxInlineBody = 8 << 20

// takeUploadArg claims the handle an html_file argument names.
//
// The value must be the URI the upload endpoint returned. A workspace path
// reaching this point means the client did not upload, and the server cannot
// read the agent's disk — saying so is more useful than "not found".
func (s *Server) takeUploadArg(ctx context.Context, owner, value string) (*store.Upload, error) {
	value = strings.TrimSpace(value)
	id := strings.TrimPrefix(value, uploadURIPrefix)
	if id == value || id == "" {
		return nil, fmt.Errorf("html_file must be the path to a workspace file: %q was not uploaded by the client", value)
	}
	return s.Store.TakeUpload(ctx, id, owner)
}

func (s *Server) readBody(ctx context.Context, v *store.Version) (string, error) {
	if v.SizeBytes > maxInlineBody {
		return "", fmt.Errorf("content is %d bytes, over the %d-byte inline limit; download version %d instead",
			v.SizeBytes, maxInlineBody, v.Seq)
	}
	reader, size, err := s.Blobs.Open(ctx, v.ContentHash)
	if err != nil {
		return "", fmt.Errorf("open content: %w", err)
	}
	defer func() { _ = reader.Close() }()
	if size > maxInlineBody {
		return "", fmt.Errorf("content is %d bytes, over the %d-byte inline limit", size, maxInlineBody)
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(reader, buf); err != nil {
		return "", fmt.Errorf("read content: %w", err)
	}
	return string(buf), nil
}

func (s *Server) callTool(w http.ResponseWriter, ctx context.Context, owner string, req rpcRequest) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeRPCError(w, req.ID, -32602, "invalid params")
		return
	}

	result, err := s.runTool(ctx, owner, params.Name, params.Arguments)
	if err != nil {
		// Tool failures are results with isError, not protocol errors: the model
		// needs to read the message and try something else.
		writeRPCResult(w, req.ID, map[string]any{
			"content": []map[string]string{{"type": "text", "text": err.Error()}},
			"isError": true,
		})
		return
	}
	writeRPCResult(w, req.ID, map[string]any{
		"content": []map[string]string{{"type": "text", "text": result}},
		"isError": false,
	})
}

func (s *Server) runTool(ctx context.Context, owner, name string, raw json.RawMessage) (string, error) {
	switch name {
	case "artifact_publish":
		var args struct {
			ArtifactID string `json:"artifact_id"`
			SpaceID    string `json:"space_id"`
			NodeID     string `json:"node_id"`
			ParentID   string `json:"parent_id"`
			Title      string `json:"title"`
			Kind       string `json:"kind"`
			HTML       string `json:"html"`
			HTMLFile   string `json:"html_file"`
			Note       string `json:"note"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
		req := publishRequest{
			ArtifactID: args.ArtifactID,
			SpaceID:    args.SpaceID,
			NodeID:     args.NodeID,
			ParentID:   args.ParentID,
			Title:      args.Title,
			Kind:       args.Kind,
			HTML:       args.HTML,
			Note:       args.Note,
		}

		var (
			a   *store.Artifact
			v   *store.Version
			err error
		)
		switch {
		case strings.TrimSpace(args.HTML) != "" && strings.TrimSpace(args.HTMLFile) != "":
			return "", fmt.Errorf("pass either html_file or html, not both")
		case strings.TrimSpace(args.HTMLFile) != "":
			up, takeErr := s.takeUploadArg(ctx, owner, args.HTMLFile)
			if takeErr != nil {
				return "", takeErr
			}
			a, v, err = s.publishUpload(ctx, owner, req, up)
		case strings.TrimSpace(args.HTML) != "":
			a, v, err = s.publish(ctx, owner, req)
		default:
			return "", fmt.Errorf("html_file is required: write the document to a file in your workspace and pass its path")
		}
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{
			"artifact_id": a.ID,
			"title":       a.Title,
			"seq":         v.Seq,
			"size_bytes":  v.SizeBytes,
			"view_url":    workViewURL(a.ID),
		})
		return string(out), nil

	case "artifact_list":
		var args struct {
			Scope string `json:"scope"`
		}
		_ = json.Unmarshal(raw, &args)
		scope := store.ScopeOwned
		if args.Scope == "shared" {
			scope = store.ScopeShared
		}
		items, err := s.Store.ListVisibleArtifacts(ctx, owner)
		_ = scope
		if err != nil {
			return "", err
		}
		rows := make([]map[string]any, 0, len(items))
		for _, a := range items {
			rows = append(rows, map[string]any{
				"artifact_id": a.ID,
				"title":       a.Title,
				"kind":        a.Kind,
				"version":     a.CurrentVersion,
				"size_bytes":  a.SizeBytes,
				"updated_at":  nowRFC3339(a.UpdatedAt),
			})
		}
		out, _ := json.Marshal(map[string]any{"artifacts": rows})
		return string(out), nil

	case "space_list":
		spaces, err := s.Store.ListSpaces(ctx, owner)
		if err != nil {
			return "", err
		}
		rows := make([]map[string]any, 0, len(spaces))
		for _, m := range spaces {
			rows = append(rows, map[string]any{
				"space_id": m.ID, "name": m.Name, "kind": m.Kind, "role": m.Role,
			})
		}
		out, _ := json.Marshal(map[string]any{"spaces": rows})
		return string(out), nil

	case "tree_get":
		var args struct {
			SpaceID string `json:"space_id"`
		}
		if err := json.Unmarshal(raw, &args); err != nil || strings.TrimSpace(args.SpaceID) == "" {
			return "", fmt.Errorf("space_id is required")
		}
		role, err := s.Store.Role(ctx, args.SpaceID, owner)
		if err != nil {
			return "", err
		}
		if role == "" {
			return "", store.ErrNotFound
		}
		nodes, err := s.Store.ListNodes(ctx, args.SpaceID)
		if err != nil {
			return "", err
		}
		rows := make([]map[string]any, 0, len(nodes))
		for _, n := range nodes {
			rows = append(rows, map[string]any{
				"node_id": n.ID, "parent_id": n.ParentID, "kind": n.Kind,
				"title": n.Title, "artifact_id": n.ArtifactID,
			})
		}
		out, _ := json.Marshal(map[string]any{"space_id": args.SpaceID, "nodes": rows})
		return string(out), nil

	case "artifact_get":
		var args struct {
			ArtifactID  string `json:"artifact_id"`
			Seq         int    `json:"seq"`
			IncludeHTML bool   `json:"include_html"`
			DownloadTo  string `json:"download_to"`
		}
		if err := json.Unmarshal(raw, &args); err != nil || strings.TrimSpace(args.ArtifactID) == "" {
			return "", fmt.Errorf("artifact_id is required")
		}
		a, err := s.Store.Get(ctx, args.ArtifactID)
		if err != nil {
			return "", err
		}
		// Space membership counts here, exactly as it does in the UI: this used
		// to ask CanView, which only knows the owner and per-artifact grants, so
		// a member could read a document in the browser but not through MCP.
		ok, err := s.Store.CanViewArtifact(ctx, a, owner)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", store.ErrNotFound
		}
		versions, err := s.Store.Versions(ctx, a.ID)
		if err != nil {
			return "", err
		}
		rows := make([]map[string]any, 0, len(versions))
		for _, v := range versions {
			rows = append(rows, map[string]any{
				"seq": v.Seq, "size_bytes": v.SizeBytes, "note": v.Note,
				"created_at": nowRFC3339(v.CreatedAt),
			})
		}
		spaceID, nodeID := "", ""
		if node, err := s.Store.NodeForArtifact(ctx, a.ID); err == nil {
			spaceID, nodeID = node.SpaceID, node.ID
		}

		payload := map[string]any{
			"artifact_id": a.ID, "title": a.Title, "kind": a.Kind,
			"space_id": spaceID, "node_id": nodeID,
			"current_version": a.CurrentVersion, "versions": rows,
		}

		// The body is opt-in: most callers want the shape of the work, and a
		// 5 MB HTML document in every response would be waste. When asked for,
		// the cap protects the JSON-RPC channel from a file it cannot carry.
		//
		// download_to is the same idea taken one step further: the agent asks
		// for a place to put the bytes, the client fetches them from raw_path
		// and writes the file, and the document stays out of the transcript.
		if args.IncludeHTML && strings.TrimSpace(args.DownloadTo) != "" {
			return "", fmt.Errorf("pass either download_to or include_html, not both")
		}
		if strings.TrimSpace(args.DownloadTo) != "" {
			seq := args.Seq
			if seq == 0 {
				seq = a.CurrentVersion
			}
			version, err := s.Store.VersionAt(ctx, a.ID, seq)
			if err != nil {
				return "", err
			}
			payload["download"] = map[string]any{
				"artifact_id":  a.ID,
				"seq":          version.Seq,
				"size_bytes":   version.SizeBytes,
				"content_hash": version.ContentHash,
				"raw_path":     fmt.Sprintf("/mcp/artifacts/%s/versions/%d/raw", a.ID, version.Seq),
			}
			out, _ := json.Marshal(payload)
			return string(out), nil
		}

		if args.IncludeHTML {
			seq := args.Seq
			if seq == 0 {
				seq = a.CurrentVersion
			}
			version, err := s.Store.VersionAt(ctx, a.ID, seq)
			if err != nil {
				return "", err
			}
			html, err := s.readBody(ctx, version)
			if err != nil {
				return "", err
			}
			payload["seq"] = version.Seq
			payload["content_hash"] = version.ContentHash
			payload["size_bytes"] = version.SizeBytes
			payload["html"] = html
		}

		out, _ := json.Marshal(payload)
		return string(out), nil

	case "artifact_rename":
		var args struct {
			ArtifactID string `json:"artifact_id"`
			NodeID     string `json:"node_id"`
			Title      string `json:"title"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
		title := strings.TrimSpace(args.Title)
		if title == "" {
			return "", fmt.Errorf("title is required")
		}

		// The library shows nodes.title; the work's own title is metadata. A
		// rename has to reach the tree row too, so resolve that row from whichever
		// id the caller named — and refuse before writing, not after.
		var node *store.Node
		switch {
		case strings.TrimSpace(args.NodeID) != "":
			found, err := s.Store.NodeByID(ctx, args.NodeID)
			if err != nil {
				return "", err
			}
			node = found
		case strings.TrimSpace(args.ArtifactID) != "":
			found, err := s.Store.NodeForArtifact(ctx, args.ArtifactID)
			switch {
			case errors.Is(err, store.ErrNotFound):
				// A work that is not filed in any tree: nothing to rename there.
			case err != nil:
				return "", err
			default:
				node = found
			}
		}

		if node != nil {
			role, err := s.Store.Role(ctx, node.SpaceID, owner)
			if err != nil {
				return "", err
			}
			// Renaming is an edit: a viewer of the space may read but not rename.
			if role != store.RoleOwner && role != store.RoleEditor {
				return "", store.ErrNotFound
			}
		}

		artifactID := strings.TrimSpace(args.ArtifactID)
		if node != nil && node.ArtifactID != "" {
			artifactID = node.ArtifactID
		}
		if node == nil && artifactID == "" {
			return "", fmt.Errorf("node_id or artifact_id is required")
		}

		treeUpdated := false
		if artifactID != "" {
			a, err := s.Store.Get(ctx, artifactID)
			if err != nil {
				return "", err
			}
			// Without a node the only credential is ownership: an artifact that
			// hangs off a tree was already checked above.
			if node == nil && a.OwnerUserID != owner {
				return "", store.ErrNotFound
			}
			nodes, err := s.Store.RenameWork(ctx, artifactID, title)
			if err != nil {
				return "", err
			}
			treeUpdated = nodes > 0
		}
		// A node that holds no work yet (an empty document) is a tree-only edit.
		if node != nil && node.ArtifactID == "" {
			if _, err := s.Store.UpdateNode(ctx, node.ID, &title, nil); err != nil {
				return "", err
			}
			treeUpdated = true
		}

		nodeID := ""
		if node != nil {
			nodeID = node.ID
		}
		out, _ := json.Marshal(map[string]any{
			"artifact_id":  artifactID,
			"node_id":      nodeID,
			"title":        title,
			"tree_updated": treeUpdated,
		})
		return string(out), nil

	case "artifact_grant":
		var args struct {
			ArtifactID string `json:"artifact_id"`
			UserID     string `json:"user_id"`
			Email      string `json:"email"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
		target := args.UserID
		if target == "" && args.Email != "" {
			user, err := s.Identity.FindByEmail(ctx, args.Email)
			if err != nil {
				return "", err
			}
			if user == nil {
				return "", fmt.Errorf("no account with that email")
			}
			target = user.ID
		}
		if err := s.Store.Grant(ctx, args.ArtifactID, owner, target); err != nil {
			return "", err
		}
		return `{"granted":true}`, nil

	case "artifact_delete":
		var args struct {
			ArtifactID string `json:"artifact_id"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
		if err := s.Store.SoftDelete(ctx, args.ArtifactID, owner); err != nil {
			return "", err
		}
		return `{"deleted":true}`, nil
	}
	return "", fmt.Errorf("unknown tool %q", name)
}

func writeRPCResult(w http.ResponseWriter, id json.RawMessage, result any) {
	if len(id) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	writeJSON(w, http.StatusOK, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": message},
	})
}
