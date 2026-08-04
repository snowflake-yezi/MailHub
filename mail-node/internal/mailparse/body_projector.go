package mailparse

import (
	"fmt"
	"mime"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/jhillyerd/enmime"
	nethtml "golang.org/x/net/html"
)

type projectionOutcome struct {
	text          string
	html          string
	externalParts []externalPart
	primaryView   *BodyView
	bodyViews     []BodyView
	parts         []ProjectedPart
	status        ParseStatus
	errorCode     string
	warnings      []ParseWarning
}

type bodyCandidate struct {
	plain           *mimeNode
	html            *mimeNode
	selected        *mimeNode
	relatedRoot     *mimeNode
	supportingPlain []*mimeNode
	views           []BodyView
	relatedScopes   []relatedScope
}

type relatedScope struct {
	container *mimeNode
	root      *mimeNode
}

type alternativeBranch struct {
	candidate   *bodyCandidate
	diagnostics *projector
}

type projector struct {
	limits         Limits
	warnings       *warningCollector
	issues         map[string]ParseStatus
	referenceCount int
}

func projectEnvelope(envelope *enmime.Envelope, limits Limits) projectionOutcome {
	externalParts := legacyExternalParts(envelope)
	warnings := newWarningCollector(limits.MaxWarnings)
	projector := &projector{
		limits:   limits,
		warnings: warnings,
		issues:   make(map[string]ParseStatus),
	}
	tree, limitCode := buildMIMETree(envelope, limits, warnings, externalParts)
	if tree == nil {
		projector.addIssue(ParseFailed, "mime_parse_failed")
		return projector.outcome(nil, nil, externalParts)
	}
	projector.collectParserWarnings(tree)
	if limitCode != "" {
		projector.addIssue(ParseTooLarge, limitCode)
		return projector.outcome(tree, nil, externalParts)
	}

	candidate := projector.candidateFor(tree.root)
	if candidate != nil {
		projector.applyCandidateRoles(candidate)
		projector.resolveReferences(candidate)
		externalParts = appendReferencedRelatedParts(externalParts, tree, limits, warnings)
	}
	return projector.outcome(tree, candidate, externalParts)
}

func (projector *projector) outcome(tree *mimeTree, candidate *bodyCandidate, externalParts []externalPart) projectionOutcome {
	outcome := projectionOutcome{
		externalParts: append([]externalPart(nil), externalParts...),
		bodyViews:     []BodyView{},
		parts:         []ProjectedPart{},
		status:        ParseOK,
		warnings:      projector.warnings.values(),
	}
	status := dominantStatus(projector.issues)
	if tree != nil && status != ParseTooLarge && status != ParseFailed {
		outcome.parts = tree.projectedParts()
	}
	if candidate != nil && status != ParseTooLarge && status != ParseFailed {
		outcome.text, outcome.html = projector.bodyContent(candidate)
		outcome.bodyViews = append([]BodyView(nil), candidate.views...)
		if len(outcome.bodyViews) == 0 {
			outcome.bodyViews = append(outcome.bodyViews, bodyViewFor("group:0", candidate))
		}
		primary := outcome.bodyViews[0]
		outcome.primaryView = &primary
	}
	outcome.status, outcome.errorCode = dominantIssue(projector.issues)
	outcome.warnings = projector.warnings.values()
	return outcome
}

func (projector *projector) candidateFor(node *mimeNode) *bodyCandidate {
	if node == nil || hasAttachmentDisposition(node) {
		return nil
	}
	contentType := normalizedContentType(node.part.ContentType)
	if len(node.children) == 0 {
		switch contentType {
		case "text/plain":
			return &bodyCandidate{plain: node, selected: node, supportingPlain: []*mimeNode{}, views: []BodyView{}, relatedScopes: []relatedScope{}}
		case "text/html":
			return &bodyCandidate{html: node, selected: node, supportingPlain: []*mimeNode{}, views: []BodyView{}, relatedScopes: []relatedScope{}}
		case "message/rfc822", "message/global":
			projector.setRole(node, RoleEmbeddedMessage)
			return nil
		default:
			return nil
		}
	}

	switch contentType {
	case "multipart/alternative":
		return projector.projectAlternative(node)
	case "multipart/related":
		return projector.projectRelated(node)
	case "multipart/signed":
		return projector.projectSigned(node)
	case "multipart/encrypted":
		projector.markChildren(node, RoleEncrypted)
		projector.warnings.add("unsupported_encrypted_body", node.path)
		projector.addIssue(ParsePartial, "unsupported_encrypted_body")
		return nil
	case "multipart/report":
		return projector.projectReport(node)
	case "multipart/digest":
		projector.markChildren(node, RoleEmbeddedMessage)
		return nil
	case "message/rfc822", "message/global":
		projector.markSubtree(node, RoleEmbeddedMessage)
		return nil
	default:
		if strings.HasPrefix(contentType, "multipart/") {
			return projector.projectMixed(node)
		}
		return nil
	}
}

func (p *projector) projectAlternative(node *mimeNode) *bodyCandidate {
	branches := make([]alternativeBranch, len(node.children))
	var selected *bodyCandidate
	selectedIndex := -1
	plainCount := 0
	htmlCount := 0
	for index, child := range node.children {
		branchProjector := &projector{
			limits:   p.limits,
			warnings: newWarningCollector(p.limits.MaxWarnings),
			issues:   make(map[string]ParseStatus),
		}
		candidate := branchProjector.candidateFor(child)
		branches[index] = alternativeBranch{candidate: candidate, diagnostics: branchProjector}
		if candidate == nil {
			continue
		}
		if candidate.plain != nil {
			plainCount++
		}
		if candidate.html != nil {
			htmlCount++
		}
	}
	for index := len(branches) - 1; index >= 0; index-- {
		if branches[index].candidate != nil && branches[index].candidate.selected != nil {
			selected = branches[index].candidate
			selectedIndex = index
			break
		}
	}
	if selected == nil {
		for _, branch := range branches {
			p.mergeDiagnostics(branch.diagnostics)
		}
		return nil
	}

	result := cloneCandidate(selected)
	contributors := map[int]struct{}{selectedIndex: {}}
	for index := len(branches) - 1; index >= 0; index-- {
		candidate := branches[index].candidate
		if candidate == nil {
			continue
		}
		if result.plain == nil && candidate.plain != nil {
			result.plain = candidate.plain
			contributors[index] = struct{}{}
		}
		if result.html == nil && candidate.html != nil {
			result.html = candidate.html
			contributors[index] = struct{}{}
		}
	}
	for index, branch := range branches {
		if _, contributes := contributors[index]; contributes {
			p.mergeDiagnostics(branch.diagnostics)
		}
	}
	if plainCount > 1 || htmlCount > 1 {
		p.warnings.add("duplicate_alternative_type", node.path)
		p.addIssue(ParsePartial, "mime_partial")
	}
	view := bodyViewFor("group:"+partPathString(node.path), result)
	result.views = append([]BodyView{view}, result.views...)
	return result
}

func (projector *projector) projectRelated(node *mimeNode) *bodyCandidate {
	if len(node.children) == 0 {
		projector.warnings.add("missing_related_root", node.path)
		projector.addIssue(ParsePartial, "mime_partial")
		return nil
	}

	root := node.children[0]
	start := relatedStart(node.part)
	if start != "" {
		root = nil
		for _, child := range node.children {
			if canonicalContentID(child.part.ContentID) == start {
				root = child
				break
			}
		}
		if root == nil {
			projector.warnings.add("missing_related_root", node.path)
			projector.addIssue(ParsePartial, "mime_partial")
			root = node.children[0]
		}
	}

	candidate := projector.candidateFor(root)
	if candidate == nil {
		for _, child := range node.children {
			fallback := projector.candidateFor(child)
			if fallback != nil {
				root = child
				candidate = fallback
				projector.warnings.add("related_root_fallback", node.path)
				projector.addIssue(ParsePartial, "mime_partial")
				break
			}
		}
	}
	if candidate == nil {
		return nil
	}
	candidate = cloneCandidate(candidate)
	candidate.relatedRoot = root
	candidate.relatedScopes = append(candidate.relatedScopes, relatedScope{container: node, root: root})
	for index := range candidate.views {
		candidate.views[index].RelatedRoot = clonePartPath(root.path)
	}
	return candidate
}

func (projector *projector) projectMixed(node *mimeNode) *bodyCandidate {
	var primary *bodyCandidate
	for _, child := range node.children {
		candidate := projector.candidateFor(child)
		if candidate == nil {
			continue
		}
		if primary == nil {
			primary = cloneCandidate(candidate)
			continue
		}
		if candidate.plain != nil {
			primary.supportingPlain = append(primary.supportingPlain, candidate.plain)
			primary.relatedScopes = append(primary.relatedScopes, candidate.relatedScopes...)
		}
	}
	return primary
}

func (projector *projector) projectSigned(node *mimeNode) *bodyCandidate {
	if len(node.children) == 0 {
		return nil
	}
	for _, child := range node.children[1:] {
		projector.markSubtree(child, RoleSignature)
	}
	return projector.candidateFor(node.children[0])
}

func (projector *projector) projectReport(node *mimeNode) *bodyCandidate {
	if len(node.children) == 0 {
		return nil
	}
	for _, child := range node.children[1:] {
		projector.markSubtree(child, RoleReport)
	}
	return projector.candidateFor(node.children[0])
}

func (projector *projector) applyCandidateRoles(candidate *bodyCandidate) {
	if candidate.plain != nil {
		projector.setRole(candidate.plain, RoleBodyPlain)
	}
	if candidate.html != nil {
		projector.setRole(candidate.html, RoleBodyHTML)
	}
	for _, supporting := range candidate.supportingPlain {
		projector.setRole(supporting, RoleBodyPlain)
	}
	for _, scope := range candidate.relatedScopes {
		for _, child := range scope.container.children {
			if child != scope.root {
				projector.markSubtree(child, RoleRelatedResource)
			}
		}
	}
}

func (projector *projector) resolveReferences(candidate *bodyCandidate) {
	resolvedHTML := make(map[*mimeNode]struct{})
	for _, scope := range candidate.relatedScopes {
		htmlNodes := make([]*mimeNode, 0)
		projector.walk(scope.root, func(node *mimeNode) {
			if node.role == RoleBodyHTML {
				htmlNodes = append(htmlNodes, node)
			}
		})
		resources := make([]*mimeNode, 0)
		projector.walk(scope.container, func(node *mimeNode) {
			if node.role == RoleRelatedResource && canonicalContentID(node.part.ContentID) != "" {
				resources = append(resources, node)
			}
		})
		for _, htmlNode := range htmlNodes {
			if _, alreadyResolved := resolvedHTML[htmlNode]; alreadyResolved {
				continue
			}
			resolvedHTML[htmlNode] = struct{}{}
			for _, reference := range projector.htmlCIDReferences(htmlNode) {
				matches := exactCIDMatches(resources, reference)
				if len(matches) == 0 {
					matches = foldedCIDMatches(resources, reference)
				}
				switch len(matches) {
				case 0:
					projector.warnings.add("unresolved_cid", htmlNode.path)
					projector.addIssue(ParsePartial, "mime_partial")
				case 1:
					appendReferencedBy(matches[0], htmlNode.path)
				default:
					projector.warnings.add("ambiguous_content_id", htmlNode.path)
					projector.addIssue(ParsePartial, "mime_partial")
				}
			}
		}
		for _, resource := range resources {
			if len(resource.referencedBy) == 0 {
				projector.warnings.add("unreferenced_related_part", resource.path)
			}
		}
	}
}

func (projector *projector) htmlCIDReferences(node *mimeNode) []string {
	result := make([]string, 0)
	tokenizer := nethtml.NewTokenizer(strings.NewReader(string(node.part.Content)))
	for {
		switch tokenizer.Next() {
		case nethtml.ErrorToken:
			return result
		case nethtml.StartTagToken, nethtml.SelfClosingTagToken:
			token := tokenizer.Token()
			for _, attribute := range token.Attr {
				if !strings.EqualFold(attribute.Key, "src") && !strings.EqualFold(attribute.Key, "poster") {
					continue
				}
				value := strings.TrimSpace(attribute.Val)
				if len(value) < 4 || !strings.EqualFold(value[:4], "cid:") {
					continue
				}
				if len(value) > projector.limits.MaxReferenceBytes {
					projector.referenceLimit(node.path)
					continue
				}
				if projector.referenceCount >= projector.limits.MaxReferences {
					projector.referenceLimit(node.path)
					continue
				}
				projector.referenceCount++
				if reference := canonicalCIDReference(value); reference != "" {
					result = append(result, reference)
				}
			}
		}
	}
}

func (projector *projector) referenceLimit(path PartPath) {
	projector.warnings.add("reference_limit_exceeded", path)
	projector.addIssue(ParsePartial, "mime_partial")
}

func (projector *projector) walk(node *mimeNode, visit func(*mimeNode)) {
	if node == nil {
		return
	}
	visit(node)
	for _, child := range node.children {
		projector.walk(child, visit)
	}
}

func exactCIDMatches(resources []*mimeNode, reference string) []*mimeNode {
	result := make([]*mimeNode, 0, 1)
	for _, resource := range resources {
		if canonicalContentID(resource.part.ContentID) == reference {
			result = append(result, resource)
		}
	}
	return result
}

func foldedCIDMatches(resources []*mimeNode, reference string) []*mimeNode {
	result := make([]*mimeNode, 0, 1)
	for _, resource := range resources {
		if strings.EqualFold(canonicalContentID(resource.part.ContentID), reference) {
			result = append(result, resource)
		}
	}
	return result
}

func canonicalCIDReference(value string) string {
	value = strings.TrimSpace(value)
	if len(value) < 4 || !strings.EqualFold(value[:4], "cid:") {
		return ""
	}
	value = value[4:]
	if decoded, err := url.PathUnescape(value); err == nil {
		value = decoded
	}
	return canonicalContentID(value)
}

func appendReferencedBy(node *mimeNode, path PartPath) {
	for _, existing := range node.referencedBy {
		if partPathsEqual(existing, path) {
			return
		}
	}
	node.referencedBy = append(node.referencedBy, clonePartPath(path))
}

func partPathsEqual(left, right PartPath) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (projector *projector) bodyContent(candidate *bodyCandidate) (string, string) {
	textBody := ""
	htmlBody := ""
	if candidate.plain != nil {
		textBody = strings.TrimSpace(string(candidate.plain.part.Content))
	}
	if candidate.html != nil {
		htmlBody = strings.TrimSpace(string(candidate.html.part.Content))
	}
	if textBody == "" && htmlBody != "" {
		textBody = HTMLToPlainText(htmlBody)
		projector.warnings.add("plain_generated_from_html", candidate.html.path)
	}
	for _, supporting := range candidate.supportingPlain {
		value := strings.TrimSpace(string(supporting.part.Content))
		if value == "" {
			continue
		}
		if textBody != "" {
			textBody += "\n--\n"
		}
		textBody += value
	}
	if len(textBody) > projector.limits.MaxTextBytes {
		textBody = ""
		path := PartPath{}
		if candidate.plain != nil {
			path = candidate.plain.path
		}
		projector.warnings.add("body_size_limit_exceeded", path)
		projector.addIssue(ParsePartial, "body_size_limit_exceeded")
	}
	if len(htmlBody) > projector.limits.MaxHTMLBytes {
		htmlBody = ""
		path := PartPath{}
		if candidate.html != nil {
			path = candidate.html.path
		}
		projector.warnings.add("body_size_limit_exceeded", path)
		projector.addIssue(ParsePartial, "body_size_limit_exceeded")
	}
	return textBody, htmlBody
}

func (projector *projector) collectParserWarnings(tree *mimeTree) {
	for _, node := range tree.nodes {
		for _, problem := range node.part.Errors {
			switch problem.Name {
			case enmime.ErrorPlainTextFromHTML:
				continue
			case enmime.ErrorCharsetConversion, enmime.ErrorCharsetDeclaration:
				projector.warnings.add("charset_decode_failed", node.path)
				projector.addIssue(ParsePartial, "mime_partial")
			case enmime.ErrorMissingBoundary:
				projector.warnings.add("missing_boundary", node.path)
				if node == tree.root {
					projector.addIssue(ParseFailed, "mime_parse_failed")
				} else {
					projector.addIssue(ParsePartial, "mime_partial")
				}
			case enmime.ErrorMalformedChildPart:
				projector.warnings.add("malformed_child_part", node.path)
				projector.addIssue(ParsePartial, "mime_partial")
			default:
				if problem.Severe {
					projector.warnings.add("mime_parse_failed", node.path)
					projector.addIssue(ParsePartial, "mime_partial")
				}
			}
		}
	}
}

func (projector *projector) markChildren(node *mimeNode, role PartRole) {
	for _, child := range node.children {
		projector.markSubtree(child, role)
	}
}

func (projector *projector) markSubtree(node *mimeNode, role PartRole) {
	if node == nil {
		return
	}
	projector.setRole(node, role)
	for _, child := range node.children {
		projector.markSubtree(child, role)
	}
}

func (projector *projector) setRole(node *mimeNode, role PartRole) {
	if node == nil || hasAttachmentDisposition(node) {
		return
	}
	node.role = role
}

func (projector *projector) addIssue(status ParseStatus, code string) {
	if code == "" {
		return
	}
	if existing, ok := projector.issues[code]; !ok || statusRank(status) > statusRank(existing) {
		projector.issues[code] = status
	}
}

func (projector *projector) mergeDiagnostics(source *projector) {
	if source == nil {
		return
	}
	projector.warnings.merge(source.warnings)
	for code, status := range source.issues {
		projector.addIssue(status, code)
	}
}

func cloneCandidate(candidate *bodyCandidate) *bodyCandidate {
	if candidate == nil {
		return nil
	}
	return &bodyCandidate{
		plain:           candidate.plain,
		html:            candidate.html,
		selected:        candidate.selected,
		relatedRoot:     candidate.relatedRoot,
		supportingPlain: append([]*mimeNode(nil), candidate.supportingPlain...),
		views:           append([]BodyView(nil), candidate.views...),
		relatedScopes:   append([]relatedScope(nil), candidate.relatedScopes...),
	}
}

func bodyViewFor(groupID string, candidate *bodyCandidate) BodyView {
	view := BodyView{GroupID: groupID}
	if candidate.plain != nil {
		view.PlainPath = clonePartPath(candidate.plain.path)
	}
	if candidate.html != nil {
		view.HTMLPath = clonePartPath(candidate.html.path)
	}
	if candidate.selected != nil {
		view.SelectedPath = clonePartPath(candidate.selected.path)
	}
	if candidate.relatedRoot != nil {
		view.RelatedRoot = clonePartPath(candidate.relatedRoot.path)
	}
	return view
}

func relatedStart(part *enmime.Part) string {
	if part == nil {
		return ""
	}
	_, params, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
	if err != nil {
		return ""
	}
	return canonicalContentID(params["start"])
}

func canonicalContentID(value string) string {
	return strings.Trim(strings.TrimSpace(value), "<>")
}

func hasAttachmentDisposition(node *mimeNode) bool {
	return node != nil && strings.EqualFold(strings.TrimSpace(node.part.Disposition), "attachment")
}

func partPathString(path PartPath) string {
	if len(path) == 0 {
		return "0"
	}
	parts := make([]string, 0, len(path)+1)
	parts = append(parts, "0")
	for _, index := range path {
		parts = append(parts, strconv.Itoa(index))
	}
	return strings.Join(parts, ".")
}

var errorCodePriority = []string{
	"parser_panic",
	"mime_parse_failed",
	"message_size_limit_exceeded",
	"mime_depth_limit_exceeded",
	"part_count_limit_exceeded",
	"decoded_size_limit_exceeded",
	"part_size_limit_exceeded",
	"body_size_limit_exceeded",
	"unsupported_encrypted_body",
	"mime_partial",
}

func dominantIssue(issues map[string]ParseStatus) (ParseStatus, string) {
	status := dominantStatus(issues)
	if status == ParseOK {
		return ParseOK, ""
	}
	for _, code := range errorCodePriority {
		if issues[code] == status {
			return status, code
		}
	}
	return status, "mime_partial"
}

func dominantStatus(issues map[string]ParseStatus) ParseStatus {
	status := ParseOK
	for _, candidate := range issues {
		if statusRank(candidate) > statusRank(status) {
			status = candidate
		}
	}
	return status
}

func statusRank(status ParseStatus) int {
	switch status {
	case ParseFailed:
		return 3
	case ParseTooLarge:
		return 2
	case ParsePartial:
		return 1
	default:
		return 0
	}
}

type warningCollector struct {
	max         int
	valuesByKey map[string]ParseWarning
	overflow    bool
}

func newWarningCollector(max int) *warningCollector {
	return &warningCollector{max: max, valuesByKey: make(map[string]ParseWarning)}
}

func (collector *warningCollector) add(code string, path PartPath) {
	if code == "" || collector.max <= 0 {
		return
	}
	key := code + "@" + partPathString(path)
	if _, exists := collector.valuesByKey[key]; exists {
		return
	}
	if len(collector.valuesByKey) >= collector.max {
		collector.overflow = true
		return
	}
	collector.valuesByKey[key] = ParseWarning{Code: code, Path: clonePartPath(path)}
}

func (collector *warningCollector) merge(source *warningCollector) {
	if source == nil {
		return
	}
	warnings := make([]ParseWarning, 0, len(source.valuesByKey))
	for _, warning := range source.valuesByKey {
		warnings = append(warnings, warning)
	}
	sort.Slice(warnings, func(i, j int) bool {
		left := partPathString(warnings[i].Path) + "@" + warnings[i].Code
		right := partPathString(warnings[j].Path) + "@" + warnings[j].Code
		return left < right
	})
	for _, warning := range warnings {
		collector.add(warning.Code, warning.Path)
	}
	if source.overflow {
		collector.overflow = true
	}
}

func (collector *warningCollector) values() []ParseWarning {
	values := make([]ParseWarning, 0, len(collector.valuesByKey))
	for _, warning := range collector.valuesByKey {
		values = append(values, warning)
	}
	sort.Slice(values, func(i, j int) bool {
		left := partPathString(values[i].Path) + "@" + values[i].Code
		right := partPathString(values[j].Path) + "@" + values[j].Code
		return left < right
	})
	if collector.overflow && collector.max > 0 {
		limitWarning := ParseWarning{Code: "warning_limit_exceeded", Path: PartPath{}}
		if len(values) == collector.max {
			values[len(values)-1] = limitWarning
		} else {
			values = append(values, limitWarning)
		}
	}
	return values
}

func (warning ParseWarning) String() string {
	return fmt.Sprintf("%s@%s", warning.Code, partPathString(warning.Path))
}
