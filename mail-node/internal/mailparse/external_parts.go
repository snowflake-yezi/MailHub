package mailparse

import "github.com/jhillyerd/enmime"

type externalPart struct {
	part   *enmime.Part
	inline bool
}

func legacyExternalParts(envelope *enmime.Envelope) []externalPart {
	if envelope == nil {
		return []externalPart{}
	}

	inlineContentIDs := HTMLCIDReferences(envelope.HTML)
	parts := make([]externalPart, 0, len(envelope.Attachments)+len(envelope.Inlines))
	for _, part := range envelope.Attachments {
		parts = append(parts, externalPart{part: part, inline: IsInlinePart(part, inlineContentIDs)})
	}
	for _, part := range envelope.Inlines {
		parts = append(parts, externalPart{part: part, inline: true})
	}
	return parts
}

func appendReferencedRelatedParts(parts []externalPart, tree *mimeTree, limits Limits, warnings *warningCollector) []externalPart {
	result := append([]externalPart(nil), parts...)
	if tree == nil {
		return result
	}

	seen := make(map[*enmime.Part]struct{}, len(result))
	for _, part := range result {
		seen[part.part] = struct{}{}
	}

	for _, node := range tree.nodes {
		if node == nil || node.part == nil || node.role != RoleRelatedResource || len(node.children) != 0 || len(node.referencedBy) == 0 {
			continue
		}
		if _, exists := seen[node.part]; exists {
			continue
		}
		if len(result) >= limits.MaxAttachments {
			warnings.add("attachment_limit_exceeded", node.path)
			break
		}

		index := len(result)
		node.externalIndex = &index
		result = append(result, externalPart{part: node.part, inline: true})
		seen[node.part] = struct{}{}
	}
	return result
}

func externalPartPointers(parts []externalPart) []*enmime.Part {
	result := make([]*enmime.Part, 0, len(parts))
	for _, part := range parts {
		result = append(result, part.part)
	}
	return result
}

func parsedAttachments(parts []externalPart) []ParsedAttachment {
	result := make([]ParsedAttachment, 0, len(parts))
	for index, part := range parts {
		result = append(result, attachmentFromPart(index, part.part, part.inline))
	}
	return result
}
