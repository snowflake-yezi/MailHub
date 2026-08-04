package mailparse

import (
	"strings"

	"github.com/jhillyerd/enmime"
)

type mimeNode struct {
	part          *enmime.Part
	path          PartPath
	parentPath    PartPath
	children      []*mimeNode
	role          PartRole
	externalIndex *int
	referencedBy  []PartPath
}

type mimeTree struct {
	root   *mimeNode
	nodes  []*mimeNode
	byPart map[*enmime.Part]*mimeNode
}

func buildMIMETree(envelope *enmime.Envelope, limits Limits, warnings *warningCollector, externalParts []externalPart) (*mimeTree, string) {
	if envelope == nil || envelope.Root == nil {
		return nil, "mime_parse_failed"
	}

	externalIndexes := make(map[*enmime.Part]int)
	for index, externalPart := range externalParts {
		if _, exists := externalIndexes[externalPart.part]; !exists {
			externalIndexes[externalPart.part] = index
		}
	}

	tree := &mimeTree{nodes: make([]*mimeNode, 0), byPart: make(map[*enmime.Part]*mimeNode)}
	totalDecoded := int64(0)
	limitCode := ""
	var visit func(*enmime.Part, PartPath, PartPath, int) *mimeNode
	visit = func(part *enmime.Part, path, parentPath PartPath, depth int) *mimeNode {
		if part == nil || limitCode != "" {
			return nil
		}
		if depth > limits.MaxDepth {
			warnings.add("mime_depth_limit_exceeded", path)
			limitCode = "mime_depth_limit_exceeded"
			return nil
		}
		if len(tree.nodes) >= limits.MaxParts {
			warnings.add("part_count_limit_exceeded", parentPath)
			limitCode = "part_count_limit_exceeded"
			return nil
		}

		decodedSize := int64(len(part.Content))
		if decodedSize > limits.MaxPartBytes {
			warnings.add("part_size_limit_exceeded", path)
			limitCode = "part_size_limit_exceeded"
			return nil
		}
		if decodedSize > limits.MaxDecodedBytes-totalDecoded {
			warnings.add("decoded_size_limit_exceeded", path)
			limitCode = "decoded_size_limit_exceeded"
			return nil
		}
		totalDecoded += decodedSize

		node := &mimeNode{
			part:         part,
			path:         clonePartPath(path),
			parentPath:   clonePartPath(parentPath),
			role:         RoleUnknown,
			children:     make([]*mimeNode, 0),
			referencedBy: make([]PartPath, 0),
		}
		if index, ok := externalIndexes[part]; ok {
			indexCopy := index
			node.externalIndex = &indexCopy
			node.role = RoleAttachment
		}
		tree.nodes = append(tree.nodes, node)
		tree.byPart[part] = node

		childIndex := 0
		for child := part.FirstChild; child != nil; child = child.NextSibling {
			childPath := appendPartPath(path, childIndex)
			projectedChild := visit(child, childPath, path, depth+1)
			if projectedChild != nil {
				node.children = append(node.children, projectedChild)
			}
			childIndex++
		}
		return node
	}

	tree.root = visit(envelope.Root, PartPath{}, PartPath{}, 0)
	if limitCode != "" {
		return tree, limitCode
	}
	return tree, ""
}

func (tree *mimeTree) projectedParts() []ProjectedPart {
	parts := make([]ProjectedPart, 0, len(tree.nodes))
	for _, node := range tree.nodes {
		part := node.part
		parts = append(parts, ProjectedPart{
			Path:                clonePartPath(node.path),
			ParentPath:          clonePartPath(node.parentPath),
			Role:                node.role,
			DeclaredContentType: normalizedContentType(part.ContentType),
			Disposition:         strings.ToLower(strings.TrimSpace(part.Disposition)),
			Filename:            strings.TrimSpace(part.FileName),
			ContentID:           strings.Trim(strings.TrimSpace(part.ContentID), "<>"),
			ContentLocation:     strings.TrimSpace(part.Header.Get("Content-Location")),
			DecodedSize:         int64(len(part.Content)),
			ExternalIndex:       cloneInt(node.externalIndex),
			ReferencedBy:        clonePartPaths(node.referencedBy),
		})
	}
	return parts
}

func normalizedContentType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "text/plain"
	}
	return value
}

func clonePartPath(path PartPath) PartPath {
	cloned := make(PartPath, len(path))
	copy(cloned, path)
	return cloned
}

func appendPartPath(path PartPath, index int) PartPath {
	result := make(PartPath, len(path), len(path)+1)
	copy(result, path)
	return append(result, index)
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func clonePartPaths(paths []PartPath) []PartPath {
	result := make([]PartPath, 0, len(paths))
	for _, path := range paths {
		result = append(result, clonePartPath(path))
	}
	return result
}
