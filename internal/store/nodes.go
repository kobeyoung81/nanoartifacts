package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Node is one entry in a space's tree. Document and app nodes point at an
// artifact; folders only organise.
type Node struct {
	ID         string     `bson:"_id" json:"id"`
	SpaceID    string     `bson:"space_id" json:"space_id"`
	ParentID   string     `bson:"parent_id" json:"parent_id"`
	Kind       string     `bson:"kind" json:"kind"`
	Title      string     `bson:"title" json:"title"`
	Position   int        `bson:"position" json:"position"`
	ArtifactID string     `bson:"artifact_id,omitempty" json:"artifact_id,omitempty"`
	CreatedBy  string     `bson:"created_by" json:"created_by"`
	CreatedAt  time.Time  `bson:"created_at" json:"created_at"`
	UpdatedAt  time.Time  `bson:"updated_at" json:"updated_at"`
	DeletedAt  *time.Time `bson:"deleted_at,omitempty" json:"-"`
}

var nodeKinds = map[string]bool{"folder": true, "document": true, "app": true}

// nodeKindForArtifact maps an artifact kind to the tree node that holds it.
// Documents and decks are read as documents; UI and interactive prototypes are
// applications. Plain HTML defaults to a document.
func nodeKindForArtifact(artifactKind string) string {
	switch artifactKind {
	case "ui", "pro":
		return "app"
	default:
		return "document"
	}
}

// ListNodes returns every live node of a space, ordered for tree assembly.
func (s *Store) ListNodes(ctx context.Context, spaceID string) ([]Node, error) {
	cur, err := s.db.Collection(nodesCollection).Find(ctx,
		bson.D{{Key: "space_id", Value: spaceID}, {Key: "deleted_at", Value: nil}},
		options.Find().SetSort(bson.D{
			{Key: "parent_id", Value: 1}, {Key: "position", Value: 1}, {Key: "title", Value: 1},
		}))
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	defer func() { _ = cur.Close(ctx) }()
	// An empty space must serialise as [] rather than null: the browser then
	// gets an array it can iterate instead of a null it has to guard everywhere.
	out := []Node{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, fmt.Errorf("decode nodes: %w", err)
	}
	return out, nil
}

// NodeByID loads a live node.
func (s *Store) NodeByID(ctx context.Context, id string) (*Node, error) {
	var node Node
	err := s.db.Collection(nodesCollection).FindOne(ctx,
		bson.D{{Key: "_id", Value: id}, {Key: "deleted_at", Value: nil}}).Decode(&node)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get node: %w", err)
	}
	return &node, nil
}

// NodeForArtifact finds the node that holds an artifact, if any.
func (s *Store) NodeForArtifact(ctx context.Context, artifactID string) (*Node, error) {
	var node Node
	err := s.db.Collection(nodesCollection).FindOne(ctx, bson.D{
		{Key: "artifact_id", Value: artifactID},
		{Key: "deleted_at", Value: nil},
	}, options.FindOne().SetSort(bson.D{{Key: "created_at", Value: 1}})).Decode(&node)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find node: %w", err)
	}
	return &node, nil
}

// CreateNode appends a node under parentID (empty means the root).
func (s *Store) CreateNode(ctx context.Context, spaceID, parentID, kind, title, actor, artifactID string) (*Node, error) {
	if !nodeKinds[kind] {
		return nil, fmt.Errorf("%w: unknown node kind", ErrInvalid)
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, fmt.Errorf("%w: title is required", ErrInvalid)
	}
	if parentID != "" {
		parent, err := s.NodeByID(ctx, parentID)
		if err != nil {
			return nil, err
		}
		if parent.SpaceID != spaceID {
			return nil, fmt.Errorf("%w: parent belongs to another space", ErrInvalid)
		}
		if parent.Kind != "folder" {
			return nil, fmt.Errorf("%w: only folders can contain nodes", ErrInvalid)
		}
	}
	position, err := s.nextPosition(ctx, spaceID, parentID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	node := Node{
		ID:         newID(),
		SpaceID:    spaceID,
		ParentID:   parentID,
		Kind:       kind,
		Title:      title,
		Position:   position,
		ArtifactID: artifactID,
		CreatedBy:  actor,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if _, err := s.db.Collection(nodesCollection).InsertOne(ctx, node); err != nil {
		return nil, fmt.Errorf("create node: %w", err)
	}
	return &node, nil
}

func (s *Store) nextPosition(ctx context.Context, spaceID, parentID string) (int, error) {
	var last struct {
		Position int `bson:"position"`
	}
	err := s.db.Collection(nodesCollection).FindOne(ctx, bson.D{
		{Key: "space_id", Value: spaceID},
		{Key: "parent_id", Value: parentID},
		{Key: "deleted_at", Value: nil},
	}, options.FindOne().SetSort(bson.D{{Key: "position", Value: -1}})).Decode(&last)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("next position: %w", err)
	}
	return last.Position + 1, nil
}

// UpdateNode renames or moves a node.
//
// A move is refused when the target sits inside the node itself: that would
// detach a subtree from the root and make it unreachable.
func (s *Store) UpdateNode(ctx context.Context, nodeID string, title, parentID *string) (*Node, error) {
	node, err := s.NodeByID(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	set := bson.D{{Key: "updated_at", Value: time.Now().UTC()}}

	if title != nil {
		trimmed := strings.TrimSpace(*title)
		if trimmed == "" {
			return nil, fmt.Errorf("%w: title is required", ErrInvalid)
		}
		set = append(set, bson.E{Key: "title", Value: trimmed})
	}

	if parentID != nil && *parentID != node.ParentID {
		next := *parentID
		if next == nodeID {
			return nil, fmt.Errorf("%w: a node cannot contain itself", ErrInvalid)
		}
		if next != "" {
			parent, err := s.NodeByID(ctx, next)
			if err != nil {
				return nil, err
			}
			if parent.SpaceID != node.SpaceID {
				return nil, fmt.Errorf("%w: parent belongs to another space", ErrInvalid)
			}
			if parent.Kind != "folder" {
				return nil, fmt.Errorf("%w: only folders can contain nodes", ErrInvalid)
			}
			if inside, err := s.insideSubtree(ctx, node.ID, parent.ID); err != nil {
				return nil, err
			} else if inside {
				return nil, fmt.Errorf("%w: cannot move a node into its own subtree", ErrInvalid)
			}
		}
		position, err := s.nextPosition(ctx, node.SpaceID, next)
		if err != nil {
			return nil, err
		}
		set = append(set,
			bson.E{Key: "parent_id", Value: next},
			bson.E{Key: "position", Value: position})
	}

	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
	var updated Node
	if err := s.db.Collection(nodesCollection).FindOneAndUpdate(ctx,
		bson.D{{Key: "_id", Value: nodeID}},
		bson.D{{Key: "$set", Value: set}}, opts).Decode(&updated); err != nil {
		return nil, fmt.Errorf("update node: %w", err)
	}
	return &updated, nil
}

// insideSubtree reports whether candidate is nodeID or one of its descendants.
func (s *Store) insideSubtree(ctx context.Context, nodeID, candidate string) (bool, error) {
	seen := map[string]bool{}
	current := candidate
	for current != "" {
		if current == nodeID {
			return true, nil
		}
		if seen[current] {
			return false, fmt.Errorf("node tree contains a cycle at %s", current)
		}
		seen[current] = true
		parent, err := s.NodeByID(ctx, current)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return false, nil
			}
			return false, err
		}
		current = parent.ParentID
	}
	return false, nil
}

// MoveNode puts a node under parentID, immediately before beforeID (an empty
// beforeID appends to the end).
//
// UpdateNode can only append: it takes the next position under the new parent.
// Drag-and-drop needs to land *between* two rows, so this rewrites the whole
// sibling list 0..n-1 in one bulk write. There is no transaction on a standalone
// MongoDB, so a failure halfway can leave duplicate positions — reads sort by
// (position, _id), which keeps the tree stable and makes the call safe to retry.
func (s *Store) MoveNode(ctx context.Context, nodeID, spaceID, parentID, beforeID string) (*Node, error) {
	node, err := s.NodeByID(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	targetSpace := spaceID
	if targetSpace == "" {
		targetSpace = node.SpaceID
	}
	if parentID == nodeID {
		return nil, fmt.Errorf("%w: a node cannot contain itself", ErrInvalid)
	}
	if parentID != "" {
		parent, err := s.NodeByID(ctx, parentID)
		if err != nil {
			return nil, err
		}
		if parent.SpaceID != targetSpace {
			return nil, fmt.Errorf("%w: parent belongs to another space", ErrInvalid)
		}
		if parent.Kind != "folder" {
			return nil, fmt.Errorf("%w: only folders can contain nodes", ErrInvalid)
		}
		if inside, err := s.insideSubtree(ctx, node.ID, parent.ID); err != nil {
			return nil, err
		} else if inside {
			return nil, fmt.Errorf("%w: cannot move a node into its own subtree", ErrInvalid)
		}
	}
	if beforeID == nodeID {
		return node, nil
	}
	if beforeID != "" {
		target, err := s.NodeByID(ctx, beforeID)
		if err != nil {
			return nil, err
		}
		if target.SpaceID != node.SpaceID || target.ParentID != parentID {
			return nil, fmt.Errorf("%w: before must be a sibling", ErrInvalid)
		}
	}

	cur, err := s.db.Collection(nodesCollection).Find(ctx, bson.D{
		{Key: "space_id", Value: targetSpace},
		{Key: "parent_id", Value: parentID},
		{Key: "deleted_at", Value: nil},
	}, options.Find().SetSort(bson.D{{Key: "position", Value: 1}, {Key: "_id", Value: 1}}))
	if err != nil {
		return nil, fmt.Errorf("list siblings: %w", err)
	}
	var siblings []Node
	if err := cur.All(ctx, &siblings); err != nil {
		return nil, fmt.Errorf("decode siblings: %w", err)
	}

	order := make([]string, 0, len(siblings)+1)
	for _, sibling := range siblings {
		if sibling.ID != nodeID {
			order = append(order, sibling.ID)
		}
	}
	at := len(order)
	if beforeID != "" {
		for i, id := range order {
			if id == beforeID {
				at = i
				break
			}
		}
	}
	order = append(order, "")
	copy(order[at+1:], order[at:])
	order[at] = nodeID

	now := time.Now().UTC()

	// A folder crossing spaces takes its subtree with it: leaving children
	// pointing at a node that now lives elsewhere would orphan them.
	if targetSpace != node.SpaceID {
		ids, err := s.subtreeIDs(ctx, node.SpaceID, node.ID)
		if err != nil {
			return nil, err
		}
		if _, err := s.db.Collection(nodesCollection).UpdateMany(ctx,
			bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}},
			bson.D{{Key: "$set", Value: bson.D{
				{Key: "space_id", Value: targetSpace},
				{Key: "updated_at", Value: now},
			}}}); err != nil {
			return nil, fmt.Errorf("move subtree: %w", err)
		}
	}

	writes := make([]mongo.WriteModel, 0, len(order))
	for position, id := range order {
		set := bson.D{{Key: "position", Value: position}}
		if id == nodeID {
			set = append(set,
				bson.E{Key: "parent_id", Value: parentID},
				bson.E{Key: "updated_at", Value: now})
		}
		writes = append(writes, mongo.NewUpdateOneModel().
			SetFilter(bson.D{{Key: "_id", Value: id}}).
			SetUpdate(bson.D{{Key: "$set", Value: set}}))
	}
	if len(writes) > 0 {
		if _, err := s.db.Collection(nodesCollection).BulkWrite(ctx, writes); err != nil {
			return nil, fmt.Errorf("move node: %w", err)
		}
	}
	return s.NodeByID(ctx, nodeID)
}

// CopySubtree duplicates a node and everything under it into another space.
//
// Each document node gets its own artifact row (DuplicateArtifact), so the copy
// can be renamed, republished and shared independently while the bytes stay
// shared. Folders are recreated so the shape survives.
func (s *Store) CopySubtree(ctx context.Context, nodeID, spaceID, parentID, actor string) (*Node, error) {
	node, err := s.NodeByID(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	target := spaceID
	if target == "" {
		target = node.SpaceID
	}

	// ListNodes already sorts by (parent_id, position, title), so appending in
	// that order keeps the copy's sibling order identical to the source's.
	all, err := s.ListNodes(ctx, node.SpaceID)
	if err != nil {
		return nil, err
	}
	children := map[string][]Node{}
	for _, item := range all {
		children[item.ParentID] = append(children[item.ParentID], item)
	}

	var copyOne func(src Node, parent string) (*Node, error)
	copyOne = func(src Node, parent string) (*Node, error) {
		artifactID := ""
		if src.ArtifactID != "" {
			duplicate, err := s.DuplicateArtifact(ctx, src.ArtifactID, actor, src.Title)
			if err != nil {
				return nil, err
			}
			artifactID = duplicate.ID
		}
		created, err := s.CreateNode(ctx, target, parent, src.Kind, src.Title, actor, artifactID)
		if err != nil {
			return nil, err
		}
		for _, child := range children[src.ID] {
			if _, err := copyOne(child, created.ID); err != nil {
				return nil, err
			}
		}
		return created, nil
	}
	return copyOne(*node, parentID)
}

// AttachArtifact points a node at an artifact (used when publishing into a
// node that was created empty).
func (s *Store) AttachArtifact(ctx context.Context, nodeID, artifactID string) error {
	if nodeID == "" || artifactID == "" {
		return nil
	}
	_, err := s.db.Collection(nodesCollection).UpdateOne(ctx,
		bson.D{{Key: "_id", Value: nodeID}},
		bson.D{{Key: "$set", Value: bson.D{
			{Key: "artifact_id", Value: artifactID},
			{Key: "updated_at", Value: time.Now().UTC()},
		}}})
	if err != nil {
		return fmt.Errorf("attach artifact: %w", err)
	}
	return nil
}

// DeleteNode soft-deletes a node and everything under it.
func (s *Store) DeleteNode(ctx context.Context, nodeID string) error {
	node, err := s.NodeByID(ctx, nodeID)
	if err != nil {
		return err
	}
	ids, err := s.subtreeIDs(ctx, node.SpaceID, node.ID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = s.db.Collection(nodesCollection).UpdateMany(ctx,
		bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "deleted_at", Value: now}}}})
	if err != nil {
		return fmt.Errorf("delete nodes: %w", err)
	}
	return nil
}

// subtreeIDs returns the node and all its descendants, breadth first.
func (s *Store) subtreeIDs(ctx context.Context, spaceID, rootID string) ([]string, error) {
	all, err := s.ListNodes(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	children := map[string][]string{}
	for _, n := range all {
		children[n.ParentID] = append(children[n.ParentID], n.ID)
	}
	out := []string{rootID}
	queue := []string{rootID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, child := range children[current] {
			out = append(out, child)
			queue = append(queue, child)
		}
	}
	return out, nil
}
