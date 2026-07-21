package filtercontract

import "time"

const SchemaVersionV1 = 1

const (
	PolicyManual = "manual"
	PolicyAd     = "ad"
)

const (
	ModeShadow   = "shadow"
	ModeEnforce  = "enforce"
	ModeDisabled = "disabled"
)

const (
	ActionAllow      = "allow"
	ActionTag        = "tag"
	ActionQuarantine = "quarantine"
)

// ConditionValue is a scalar JSON value. A zero value is encoded as null.
// Objects, arrays, and fractional numbers are deliberately excluded from v1.
type ConditionValue struct {
	kind    conditionValueKind
	text    string
	boolean bool
	integer int64
}

type conditionValueKind uint8

const (
	conditionNull conditionValueKind = iota
	conditionString
	conditionBool
	conditionInteger
)

func StringValue(value string) ConditionValue {
	return ConditionValue{kind: conditionString, text: value}
}

func BoolValue(value bool) ConditionValue {
	return ConditionValue{kind: conditionBool, boolean: value}
}

func IntegerValue(value int64) ConditionValue {
	return ConditionValue{kind: conditionInteger, integer: value}
}

func NullValue() ConditionValue { return ConditionValue{} }

func (v ConditionValue) IsNull() bool { return v.kind == conditionNull }

func (v ConditionValue) String() (string, bool) {
	return v.text, v.kind == conditionString
}

func (v ConditionValue) Bool() (bool, bool) {
	return v.boolean, v.kind == conditionBool
}

func (v ConditionValue) Integer() (int64, bool) {
	return v.integer, v.kind == conditionInteger
}

type Condition struct {
	Field    string         `json:"field"`
	Operator string         `json:"operator"`
	Value    ConditionValue `json:"value"`
	Negated  bool           `json:"negated"`
	Position int            `json:"position"`
}

type ManualRule struct {
	LogicalID  string      `json:"logical_id"`
	Name       string      `json:"name"`
	ScopeType  string      `json:"scope_type"`
	ScopeID    *uint64     `json:"scope_id"`
	Action     string      `json:"action"`
	Priority   int         `json:"priority"`
	Mode       string      `json:"mode"`
	Source     string      `json:"source"`
	Conditions []Condition `json:"conditions"`
}

type ManualBundle struct {
	SchemaVersion int          `json:"schema_version"`
	PolicyKind    string       `json:"policy_kind"`
	Revision      uint64       `json:"revision"`
	Checksum      string       `json:"checksum"`
	Rules         []ManualRule `json:"rules"`
}

type AdDetector struct {
	LogicalID       string      `json:"logical_id"`
	Name            string      `json:"name"`
	Symbol          string      `json:"symbol"`
	Mode            string      `json:"mode"`
	Source          string      `json:"source"`
	SourceReference string      `json:"source_reference"`
	Conditions      []Condition `json:"conditions"`
}

type AdComposite struct {
	LogicalID   string   `json:"logical_id"`
	Name        string   `json:"name"`
	Symbol      string   `json:"symbol"`
	Mode        string   `json:"mode"`
	ScorePolicy string   `json:"score_policy"`
	AllOf       []string `json:"all_of"`
	AnyOf       []string `json:"any_of"`
	NoneOf      []string `json:"none_of"`
}

type SymbolWeight struct {
	Symbol string `json:"symbol"`
	Score  Score  `json:"score"`
}

type AdBundle struct {
	SchemaVersion       int            `json:"schema_version"`
	PolicyKind          string         `json:"policy_kind"`
	Revision            uint64         `json:"revision"`
	Checksum            string         `json:"checksum"`
	TagThreshold        Score          `json:"tag_threshold"`
	QuarantineThreshold Score          `json:"quarantine_threshold"`
	Detectors           []AdDetector   `json:"detectors"`
	Composites          []AdComposite  `json:"composites"`
	Weights             []SymbolWeight `json:"weights"`
}

type MailAddress struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Domain  string `json:"domain"`
}

type URLFeature struct {
	Scheme      string `json:"scheme"`
	Host        string `json:"host"`
	Path        string `json:"path"`
	Occurrences int    `json:"occurrences"`
}

type AttachmentFeature struct {
	Index       int    `json:"index"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	Inline      bool   `json:"inline"`
}

type MailFeatures struct {
	MessageKey             string              `json:"message_key"`
	MessageID              string              `json:"message_id"`
	Mailbox                string              `json:"mailbox"`
	ServerID               uint64              `json:"server_id"`
	HeaderFrom             MailAddress         `json:"header_from"`
	EnvelopeFrom           *MailAddress        `json:"envelope_from"`
	ReplyTo                []MailAddress       `json:"reply_to"`
	Subject                string              `json:"subject"`
	Text                   string              `json:"text"`
	HTMLText               string              `json:"html_text"`
	Headers                map[string][]string `json:"headers"`
	URLs                   []URLFeature        `json:"urls"`
	Attachments            []AttachmentFeature `json:"attachments"`
	ListUnsubscribe        bool                `json:"list_unsubscribe"`
	ListID                 string              `json:"list_id"`
	Precedence             string              `json:"precedence"`
	FromReplyToDomainMatch *bool               `json:"from_reply_to_domain_match"`
	TrackingPixelCount     int                 `json:"tracking_pixel_count"`
	SizeBytes              int64               `json:"size_bytes"`
	ParseWarnings          []string            `json:"parse_warnings"`
}

type Evidence struct {
	Field       string `json:"field"`
	Summary     string `json:"summary"`
	Occurrences int    `json:"occurrences"`
}

type DecisionReason struct {
	LogicalID     string     `json:"logical_id"`
	Name          string     `json:"name"`
	Action        string     `json:"action"`
	MatchedFields []string   `json:"matched_fields"`
	Evidence      []Evidence `json:"evidence"`
}

type AdSymbolResult struct {
	ProducerLogicalID string     `json:"producer_logical_id"`
	Symbol            string     `json:"symbol"`
	Matched           bool       `json:"matched"`
	Weight            Score      `json:"weight"`
	SuppressedBy      []string   `json:"suppressed_by"`
	Contribution      Score      `json:"contribution"`
	OccurrenceCount   int        `json:"occurrence_count"`
	Evidence          []Evidence `json:"evidence"`
}

type ShadowResult struct {
	PolicyKind        string           `json:"policy_kind"`
	ProducerLogicalID string           `json:"producer_logical_id"`
	Symbol            string           `json:"symbol"`
	Action            string           `json:"action"`
	Score             Score            `json:"score"`
	Symbols           []AdSymbolResult `json:"symbols"`
	Evidence          []Evidence       `json:"evidence"`
}

type FilterDecision struct {
	SchemaVersion  int              `json:"schema_version"`
	DecisionKey    string           `json:"decision_key"`
	MessageKey     string           `json:"message_key"`
	ManualRevision uint64           `json:"manual_revision"`
	AdRevision     uint64           `json:"ad_revision"`
	ManualAction   string           `json:"manual_action"`
	AdAction       string           `json:"ad_action"`
	FinalAction    string           `json:"final_action"`
	AdScore        Score            `json:"ad_score"`
	Reasons        []DecisionReason `json:"reasons"`
	AdSymbols      []AdSymbolResult `json:"ad_symbols"`
	ShadowResults  []ShadowResult   `json:"shadow_results"`
	ParseWarnings  []string         `json:"parse_warnings"`
	EvaluatedAt    time.Time        `json:"evaluated_at"`
}

type ProcessingResult struct {
	Status          string `json:"status"`
	AttemptedAction string `json:"attempted_action"`
	ActualAction    string `json:"actual_action"`
	QuarantineKey   string `json:"quarantine_key"`
	ErrorCode       string `json:"error_code"`
	ErrorSummary    string `json:"error_summary"`
}

type OutboxEvent struct {
	SchemaVersion int               `json:"schema_version"`
	Phase         string            `json:"phase"`
	NodeID        uint64            `json:"node_id"`
	Mailbox       string            `json:"mailbox"`
	MessageID     string            `json:"message_id"`
	Decision      FilterDecision    `json:"decision"`
	Result        *ProcessingResult `json:"result"`
}

type ReleaseReceipt struct {
	SchemaVersion int       `json:"schema_version"`
	OperationID   string    `json:"operation_id"`
	QuarantineKey string    `json:"quarantine_key"`
	DecisionKey   string    `json:"decision_key"`
	Status        string    `json:"status"`
	SMTPDelivered bool      `json:"smtp_delivered"`
	RestoredToCur bool      `json:"restored_to_cur"`
	ForwardTarget string    `json:"forward_target"`
	ErrorCode     string    `json:"error_code"`
	ErrorSummary  string    `json:"error_summary"`
	CompletedAt   time.Time `json:"completed_at"`
}

type GoldenCase struct {
	SchemaVersion     int            `json:"schema_version"`
	CaseID            string         `json:"case_id"`
	Label             string         `json:"label"`
	Fixture           string         `json:"fixture"`
	MaildirUniqueName string         `json:"maildir_unique_name"`
	Features          MailFeatures   `json:"features"`
	Decision          FilterDecision `json:"decision"`
}
