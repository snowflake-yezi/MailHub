package model

import "time"

const FilterPolicySchemaVersionV1 = 1

// ManualFilterRevision is an immutable manual policy snapshot once published.
type ManualFilterRevision struct {
	ID            uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	Revision      uint64     `gorm:"not null;uniqueIndex:uk_manual_filter_revision" json:"revision"`
	Status        string     `gorm:"type:enum('draft','published','retired');not null;default:draft;index" json:"status"`
	BaseRevision  *uint64    `gorm:"index" json:"base_revision,omitempty"`
	SchemaVersion int        `gorm:"not null;default:1" json:"schema_version"`
	Checksum      string     `gorm:"type:char(64);not null;default:''" json:"checksum"`
	CreatedBy     string     `gorm:"size:191;not null" json:"created_by"`
	PublishedBy   string     `gorm:"size:191" json:"published_by,omitempty"`
	CreatedAt     time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt     time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
	PublishedAt   *time.Time `json:"published_at,omitempty"`

	Rules []ManualFilterRule `gorm:"foreignKey:RevisionID" json:"rules,omitempty"`
}

type ManualFilterRule struct {
	ID         uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	RevisionID uint64    `gorm:"not null;uniqueIndex:uk_manual_rule_logical;index" json:"revision_id"`
	LogicalID  string    `gorm:"size:64;not null;uniqueIndex:uk_manual_rule_logical" json:"logical_id"`
	Name       string    `gorm:"size:191;not null" json:"name"`
	ScopeType  string    `gorm:"type:enum('global','server','domain','mailbox');not null;default:global" json:"scope_type"`
	ScopeID    *uint64   `gorm:"index" json:"scope_id,omitempty"`
	Action     string    `gorm:"type:enum('allow','tag','quarantine');not null" json:"action"`
	Priority   int       `gorm:"not null;default:0;index" json:"priority"`
	Mode       string    `gorm:"type:enum('shadow','enforce','disabled');not null;default:shadow" json:"mode"`
	Source     string    `gorm:"type:enum('manual','legacy_migration','external');not null;default:manual" json:"source"`
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt  time.Time `gorm:"autoUpdateTime" json:"updated_at"`

	Conditions []ManualFilterCondition `gorm:"foreignKey:RuleID" json:"conditions,omitempty"`
}

type ManualFilterCondition struct {
	ID        uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	RuleID    uint64 `gorm:"not null;uniqueIndex:uk_manual_condition_position;index" json:"rule_id"`
	Field     string `gorm:"size:64;not null" json:"field"`
	Operator  string `gorm:"size:32;not null" json:"operator"`
	ValueText string `gorm:"type:text;not null" json:"value_text"`
	Negated   bool   `gorm:"not null;default:false" json:"negated"`
	Position  int    `gorm:"not null;uniqueIndex:uk_manual_condition_position" json:"position"`
}

// AdFilterRevision stores thresholds in milli-points. For example, 4.250 is
// stored as 4250 so checksums never depend on database decimal conversion.
type AdFilterRevision struct {
	ID                       uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	Revision                 uint64     `gorm:"not null;uniqueIndex:uk_ad_filter_revision" json:"revision"`
	Status                   string     `gorm:"type:enum('draft','published','retired');not null;default:draft;index" json:"status"`
	BaseRevision             *uint64    `gorm:"index" json:"base_revision,omitempty"`
	SchemaVersion            int        `gorm:"not null;default:1" json:"schema_version"`
	TagThresholdMilli        int64      `gorm:"not null;default:0" json:"tag_threshold_milli"`
	QuarantineThresholdMilli int64      `gorm:"not null;default:0" json:"quarantine_threshold_milli"`
	Checksum                 string     `gorm:"type:char(64);not null;default:''" json:"checksum"`
	CreatedBy                string     `gorm:"size:191;not null" json:"created_by"`
	PublishedBy              string     `gorm:"size:191" json:"published_by,omitempty"`
	CreatedAt                time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt                time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
	PublishedAt              *time.Time `json:"published_at,omitempty"`

	Detectors  []AdFilterDetector     `gorm:"foreignKey:RevisionID" json:"detectors,omitempty"`
	Composites []AdFilterComposite    `gorm:"foreignKey:RevisionID" json:"composites,omitempty"`
	Weights    []AdFilterSymbolWeight `gorm:"foreignKey:RevisionID" json:"weights,omitempty"`
}

type AdFilterDetector struct {
	ID              uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	RevisionID      uint64    `gorm:"not null;uniqueIndex:uk_ad_detector_logical;uniqueIndex:uk_ad_detector_symbol;index" json:"revision_id"`
	LogicalID       string    `gorm:"size:64;not null;uniqueIndex:uk_ad_detector_logical" json:"logical_id"`
	Symbol          string    `gorm:"size:63;not null;uniqueIndex:uk_ad_detector_symbol" json:"symbol"`
	Name            string    `gorm:"size:191;not null" json:"name"`
	Mode            string    `gorm:"type:enum('shadow','enforce','disabled');not null;default:shadow" json:"mode"`
	Source          string    `gorm:"type:enum('local','rspamd_seed','stalwart_seed','spamassassin_seed','external');not null;default:local" json:"source"`
	SourceReference string    `gorm:"size:255" json:"source_reference,omitempty"`
	CreatedAt       time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt       time.Time `gorm:"autoUpdateTime" json:"updated_at"`

	Conditions []AdFilterCondition `gorm:"foreignKey:DetectorID" json:"conditions,omitempty"`
}

type AdFilterCondition struct {
	ID         uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	DetectorID uint64 `gorm:"not null;uniqueIndex:uk_ad_condition_position;index" json:"detector_id"`
	Field      string `gorm:"size:64;not null" json:"field"`
	Operator   string `gorm:"size:32;not null" json:"operator"`
	ValueText  string `gorm:"type:text;not null" json:"value_text"`
	Negated    bool   `gorm:"not null;default:false" json:"negated"`
	Position   int    `gorm:"not null;uniqueIndex:uk_ad_condition_position" json:"position"`
}

type AdFilterComposite struct {
	ID          uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	RevisionID  uint64    `gorm:"not null;uniqueIndex:uk_ad_composite_logical;uniqueIndex:uk_ad_composite_symbol;index" json:"revision_id"`
	LogicalID   string    `gorm:"size:64;not null;uniqueIndex:uk_ad_composite_logical" json:"logical_id"`
	Symbol      string    `gorm:"size:63;not null;uniqueIndex:uk_ad_composite_symbol" json:"symbol"`
	Name        string    `gorm:"size:191;not null" json:"name"`
	Mode        string    `gorm:"type:enum('shadow','enforce','disabled');not null;default:shadow" json:"mode"`
	ScorePolicy string    `gorm:"type:enum('keep_inputs','suppress_direct_inputs');not null;default:keep_inputs" json:"score_policy"`
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime" json:"updated_at"`

	Terms []AdFilterCompositeTerm `gorm:"foreignKey:CompositeID" json:"terms,omitempty"`
}

type AdFilterCompositeTerm struct {
	ID          uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	CompositeID uint64 `gorm:"not null;uniqueIndex:uk_ad_composite_term_position;index" json:"composite_id"`
	GroupKind   string `gorm:"type:enum('all_of','any_of','none_of');not null;uniqueIndex:uk_ad_composite_term_position" json:"group_kind"`
	InputSymbol string `gorm:"size:63;not null" json:"input_symbol"`
	Position    int    `gorm:"not null;uniqueIndex:uk_ad_composite_term_position" json:"position"`
}

type AdFilterSymbolWeight struct {
	ID         uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	RevisionID uint64 `gorm:"not null;uniqueIndex:uk_ad_weight_symbol;index" json:"revision_id"`
	Symbol     string `gorm:"size:63;not null;uniqueIndex:uk_ad_weight_symbol" json:"symbol"`
	ScoreMilli int64  `gorm:"not null" json:"score_milli"`
}

// FilterDecision stores the searchable decision fields separately from the
// versioned JSON arrays. The JSON columns remain LONGTEXT for MariaDB 10.5.
type FilterDecision struct {
	ID                uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	SchemaVersion     int       `gorm:"not null;default:1" json:"schema_version"`
	DecisionKey       string    `gorm:"size:64;not null;uniqueIndex" json:"decision_key"`
	MessageKey        string    `gorm:"size:64;not null;index" json:"message_key"`
	MessageID         string    `gorm:"size:512" json:"message_id,omitempty"`
	MailboxAccountID  uint64    `gorm:"not null;index" json:"mailbox_account_id"`
	NodeID            uint64    `gorm:"not null;index" json:"node_id"`
	ManualRevision    uint64    `gorm:"not null;default:0;index" json:"manual_revision"`
	AdRevision        uint64    `gorm:"not null;default:0;index" json:"ad_revision"`
	ManualAction      string    `gorm:"type:enum('allow','tag','quarantine');not null" json:"manual_action"`
	AdAction          string    `gorm:"type:enum('allow','tag','quarantine');not null" json:"ad_action"`
	FinalAction       string    `gorm:"type:enum('allow','tag','quarantine');not null;index" json:"final_action"`
	AdScoreMilli      int64     `gorm:"not null;default:0" json:"ad_score_milli"`
	ReasonsText       string    `gorm:"type:longtext;not null" json:"reasons_text"`
	AdSymbolsText     string    `gorm:"type:longtext;not null" json:"ad_symbols_text"`
	ShadowResultsText string    `gorm:"type:longtext;not null" json:"shadow_results_text"`
	ParseWarningsText string    `gorm:"type:longtext;not null" json:"parse_warnings_text"`
	EvaluatedAt       time.Time `gorm:"not null;index" json:"evaluated_at"`
	CreatedAt         time.Time `gorm:"autoCreateTime" json:"created_at"`
}

type FilterQuarantine struct {
	ID                 uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	DecisionID         uint64     `gorm:"not null;uniqueIndex" json:"decision_id"`
	Status             string     `gorm:"type:enum('quarantined','releasing','released','release_failed','confirmed_ad','expired');not null;default:quarantined;index" json:"status"`
	QuarantineKey      string     `gorm:"size:191;not null;uniqueIndex" json:"quarantine_key"`
	OriginalMaildirKey string     `gorm:"size:255;not null" json:"original_maildir_key"`
	ExpiresAt          time.Time  `gorm:"not null;index" json:"expires_at"`
	ReviewedBy         string     `gorm:"size:191" json:"reviewed_by,omitempty"`
	ReviewedAt         *time.Time `json:"reviewed_at,omitempty"`
	FeedbackLabel      *string    `gorm:"type:enum('confirmed_ad','false_positive','uncertain')" json:"feedback_label,omitempty"`
	ReviewNote         string     `gorm:"type:text" json:"review_note,omitempty"`
	LastError          string     `gorm:"type:text" json:"last_error,omitempty"`
	CreatedAt          time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt          time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
}

// FilterActiveState intentionally starts empty. Rows are created only by an
// explicit publish transaction, never by startup migration or seeding.
type FilterActiveState struct {
	PolicyKind     string    `gorm:"primaryKey;type:enum('manual','ad')" json:"policy_kind"`
	ActiveRevision uint64    `gorm:"not null" json:"active_revision"`
	Checksum       string    `gorm:"type:char(64);not null" json:"checksum"`
	ChangedAt      time.Time `gorm:"not null" json:"changed_at"`
	ChangedBy      string    `gorm:"size:191;not null" json:"changed_by"`
}

type FilterNodeState struct {
	ID              uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	NodeID          uint64     `gorm:"not null;uniqueIndex:uk_filter_node_policy;index" json:"node_id"`
	PolicyKind      string     `gorm:"type:enum('manual','ad');not null;uniqueIndex:uk_filter_node_policy" json:"policy_kind"`
	DesiredRevision uint64     `gorm:"not null;default:0" json:"desired_revision"`
	AppliedRevision uint64     `gorm:"not null;default:0" json:"applied_revision"`
	Checksum        string     `gorm:"type:char(64);not null;default:''" json:"checksum"`
	BootID          string     `gorm:"size:64" json:"boot_id,omitempty"`
	LastError       string     `gorm:"type:text" json:"last_error,omitempty"`
	AppliedAt       *time.Time `json:"applied_at,omitempty"`
	CreatedAt       time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt       time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
}

type FilterAudit struct {
	ID          uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	PolicyKind  string    `gorm:"size:32;not null;index" json:"policy_kind"`
	Action      string    `gorm:"size:64;not null;index" json:"action"`
	EntityType  string    `gorm:"size:64;not null" json:"entity_type"`
	EntityID    string    `gorm:"size:191;not null" json:"entity_id"`
	Revision    *uint64   `gorm:"index" json:"revision,omitempty"`
	Actor       string    `gorm:"size:191;not null" json:"actor"`
	ChangesText string    `gorm:"type:longtext;not null" json:"changes_text"`
	RequestID   string    `gorm:"size:64;index" json:"request_id,omitempty"`
	CreatedAt   time.Time `gorm:"autoCreateTime;index" json:"created_at"`
}

func (ManualFilterRevision) TableName() string  { return "manual_filter_revisions" }
func (ManualFilterRule) TableName() string      { return "manual_filter_rules" }
func (ManualFilterCondition) TableName() string { return "manual_filter_conditions" }
func (AdFilterRevision) TableName() string      { return "ad_filter_revisions" }
func (AdFilterDetector) TableName() string      { return "ad_filter_detectors" }
func (AdFilterCondition) TableName() string     { return "ad_filter_conditions" }
func (AdFilterComposite) TableName() string     { return "ad_filter_composites" }
func (AdFilterCompositeTerm) TableName() string { return "ad_filter_composite_terms" }
func (AdFilterSymbolWeight) TableName() string  { return "ad_filter_symbol_weights" }
func (FilterDecision) TableName() string        { return "filter_decisions" }
func (FilterQuarantine) TableName() string      { return "filter_quarantines" }
func (FilterActiveState) TableName() string     { return "filter_active_states" }
func (FilterNodeState) TableName() string       { return "filter_node_states" }
func (FilterAudit) TableName() string           { return "filter_audits" }
