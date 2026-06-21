package db

// Tank is the item stored in tankmaze-tanks.
type Tank struct {
	TankID               string `dynamodbav:"tankId"               json:"tankId"`
	UserID               string `dynamodbav:"userId"               json:"userId"`
	Name                 string `dynamodbav:"name"                 json:"name"`
	GlobalScore          int    `dynamodbav:"globalScore"          json:"globalScore"`
	BestFinish           *int   `dynamodbav:"bestFinish"           json:"bestFinish"`
	GameDaysCount        int    `dynamodbav:"gameDaysCount"        json:"gameDaysCount"`
	LastActiveAt         int64  `dynamodbav:"lastActiveAt"         json:"lastActiveAt"`
	CreatedAt            int64  `dynamodbav:"createdAt"            json:"createdAt"`
	AuthorName           string `dynamodbav:"authorName,omitempty"           json:"authorName,omitempty"`
	ForkedFromTankID     string `dynamodbav:"forkedFromTankId,omitempty"     json:"forkedFromTankId,omitempty"`
	ForkedFromVersion    string `dynamodbav:"forkedFromVersion,omitempty"    json:"forkedFromVersion,omitempty"`
	ScoreTransferredTo   string `dynamodbav:"scoreTransferredTo,omitempty"   json:"scoreTransferredTo,omitempty"`
	ScoreTransferredFrom string `dynamodbav:"scoreTransferredFrom,omitempty" json:"scoreTransferredFrom,omitempty"`
	AvatarURL            string `dynamodbav:"avatarUrl,omitempty"            json:"avatarUrl,omitempty"`
}

// TankStats holds the mutable aggregate fields updated by the ranking-updater.
type TankStats struct {
	GlobalScore   int
	BestFinish    *int // nil clears the attribute
	GameDaysCount int
	LastActiveAt  int64
}

// VersionConfig is the stat allocation stored inside each TankVersion record.
// It intentionally omits Name (stored on the Tank record instead).
type VersionConfig struct {
	Speed       int `dynamodbav:"speed"        json:"speed"`
	SensorRange int `dynamodbav:"sensorRange"  json:"sensorRange"`
	Damage      int `dynamodbav:"damage"       json:"damage"`
	Armor       int `dynamodbav:"armor"        json:"armor"`
	FireRate    int `dynamodbav:"fireRate"     json:"fireRate"`
}

// TankVersion is the item stored in tankmaze-tank-versions.
type TankVersion struct {
	TankID               string        `dynamodbav:"tankId"                json:"tankId"`
	Version              string        `dynamodbav:"version"               json:"version"`
	VersionType          string        `dynamodbav:"versionType"           json:"versionType"`
	Config               VersionConfig `dynamodbav:"config"                json:"config"`
	WasmS3Key            string        `dynamodbav:"wasmS3Key,omitempty"   json:"wasmS3Key,omitempty"`
	SourceS3Key          string        `dynamodbav:"sourceS3Key,omitempty" json:"sourceS3Key,omitempty"`
	WasmSHA256           string        `dynamodbav:"wasmSha256,omitempty"  json:"wasmSha256,omitempty"`
	CompileStatus        string        `dynamodbav:"compileStatus"         json:"compileStatus"`
	CompileError         string        `dynamodbav:"compileError,omitempty" json:"compileError,omitempty"`
	RegisteredForGameDays []string      `dynamodbav:"registeredForGameDays,omitempty" json:"registeredForGameDays,omitempty"`
	CreatedAt            int64         `dynamodbav:"createdAt"             json:"createdAt"`
	// Major-only stats (zero value when not set)
	WinRate          float64 `dynamodbav:"winRate,omitempty"          json:"winRate"`
	MatchesPlayed    int     `dynamodbav:"matchesPlayed,omitempty"    json:"matchesPlayed"`
	AvgDamageDealt   float64 `dynamodbav:"avgDamageDealt,omitempty"   json:"avgDamageDealt"`
	AvgSurvivalTicks float64 `dynamodbav:"avgSurvivalTicks,omitempty" json:"avgSurvivalTicks"`
	// Minor-only stats
	TestMatchCount int  `dynamodbav:"testMatchCount,omitempty" json:"testMatchCount"`
	Disqualified   bool `dynamodbav:"disqualified,omitempty"   json:"disqualified"`
}

// CompileUpdate carries the fields written after a CodeBuild tank-compiler run.
type CompileUpdate struct {
	Status       string // "compiling" | "ready" | "failed"
	WasmS3Key    string // set on "ready"
	WasmSHA256   string // set on "ready"
	CompileError string // set on "failed"
}

// VersionStats holds the performance stats updated after each ranked match.
type VersionStats struct {
	WinRate          float64
	MatchesPlayed    int
	AvgDamageDealt   float64
	AvgSurvivalTicks float64
}

// MatchTank identifies the tank and version in one side of a match.
type MatchTank struct {
	TankID   string `dynamodbav:"tankId"             json:"tankId"`
	Version  string `dynamodbav:"version"            json:"version"`
	TankName string `dynamodbav:"tankName,omitempty" json:"tankName,omitempty"`
}

// MatchResult is the outcome map written when a match ends.
// Winner is nil when Reason is "both_lose".
type MatchResult struct {
	Winner       *int   `dynamodbav:"winner"       json:"winner"`
	Reason       string `dynamodbav:"reason"       json:"reason"`
	DamageA      int    `dynamodbav:"damageA"      json:"damageA"`
	DamageB      int    `dynamodbav:"damageB"      json:"damageB"`
	MovesA       int    `dynamodbav:"movesA"       json:"movesA"`
	MovesB       int    `dynamodbav:"movesB"       json:"movesB"`
	TicksElapsed int    `dynamodbav:"ticksElapsed" json:"ticksElapsed"`
	Flawless     bool   `dynamodbav:"flawless"     json:"flawless"`
}

// Match is the item stored in tankmaze-matches.
type Match struct {
	MatchID      string       `dynamodbav:"matchId"              json:"matchId"`
	MatchType    string       `dynamodbav:"matchType"            json:"matchType"`
	GameDayID    string       `dynamodbav:"gameDayId,omitempty"  json:"gameDayId,omitempty"`
	Status       string       `dynamodbav:"status"               json:"status"`
	MazeSeed     string       `dynamodbav:"mazeSeed,omitempty"   json:"mazeSeed,omitempty"`
	MapID        string       `dynamodbav:"mapId,omitempty"      json:"mapId,omitempty"`
	TankA        MatchTank    `dynamodbav:"tankA"                json:"tankA"`
	TankB        MatchTank    `dynamodbav:"tankB"                json:"tankB"`
	TickLogS3Key string       `dynamodbav:"tickLogS3Key,omitempty" json:"tickLogS3Key,omitempty"`
	Result       *MatchResult `dynamodbav:"result,omitempty"     json:"result"`
	CreatedAt    int64        `dynamodbav:"createdAt"            json:"createdAt"`
	TTL          int64        `dynamodbav:"ttl,omitempty"        json:"-"`
}

// Connection is the item stored in tankmaze-connections.
type Connection struct {
	ConnectionID string `dynamodbav:"connectionId"`
	MatchID      string `dynamodbav:"matchId"`
	TTL          int64  `dynamodbav:"ttl"`
	ReplayTick   int    `dynamodbav:"replayTick,omitempty"`
	ReplaySpeed  string `dynamodbav:"replaySpeed,omitempty"`
}

// PhaseStatus tracks the lifecycle of one Game Day tournament phase.
type PhaseStatus struct {
	Status    string `dynamodbav:"status"              json:"status"`
	StartedAt int64  `dynamodbav:"startedAt,omitempty" json:"startedAt,omitempty"`
	EndedAt   int64  `dynamodbav:"endedAt,omitempty"   json:"endedAt,omitempty"`
}

// GameDayPhases holds the status of each phase.
type GameDayPhases struct {
	RoundRobin  PhaseStatus            `dynamodbav:"roundRobin"            json:"roundRobin"`
	Elimination map[string]PhaseStatus `dynamodbav:"elimination,omitempty" json:"elimination,omitempty"`
	Final       PhaseStatus            `dynamodbav:"final"                 json:"final"`
}

// GameDaySchedule holds the timestamps for each phase.
type GameDaySchedule struct {
	RegistrationClose string   `dynamodbav:"registrationClose" json:"registrationClose"`
	RoundRobin        string   `dynamodbav:"roundRobin"        json:"roundRobin"`
	Elimination       []string `dynamodbav:"elimination,omitempty" json:"elimination,omitempty"`
	Final             string   `dynamodbav:"final"             json:"final"`
}

// BracketSlot is one tank's slot in the elimination bracket.
type BracketSlot struct {
	TankID   string `dynamodbav:"tankId"             json:"tankId"`
	Version  string `dynamodbav:"version"            json:"version"`
	Status   string `dynamodbav:"status"             json:"status"`
	TankName string `dynamodbav:"tankName,omitempty" json:"tankName,omitempty"`
	MatchID  string `dynamodbav:"matchId,omitempty"  json:"matchId,omitempty"`
}

// GroupStanding is one tank's record within a round-robin group.
type GroupStanding struct {
	TankID   string `dynamodbav:"tankId"             json:"tankId"`
	Version  string `dynamodbav:"version"            json:"version"`
	TankName string `dynamodbav:"tankName,omitempty" json:"tankName,omitempty"`
	Wins     int    `dynamodbav:"wins"               json:"wins"`
	Losses   int    `dynamodbav:"losses"             json:"losses"`
	Points   int    `dynamodbav:"points"             json:"points"`
}

// GroupMatchResult holds the outcome of one RR match for the cross-table UI.
type GroupMatchResult struct {
	TankAID string `dynamodbav:"tankAId" json:"tankAId"`
	TankBID string `dynamodbav:"tankBId" json:"tankBId"`
	MatchID string `dynamodbav:"matchId" json:"matchId"`
	// Winner is "a", "b", "both_lose", or "" (pending/unplayed).
	Winner string `dynamodbav:"winner" json:"winner"`
}

// Group is one round-robin group within a Game Day.
type Group struct {
	GroupID      string             `dynamodbav:"groupId"               json:"groupId"`
	Tanks        []MatchTank        `dynamodbav:"tanks"                 json:"tanks"`
	Standings    []GroupStanding    `dynamodbav:"standings,omitempty"   json:"standings,omitempty"`
	MatchResults []GroupMatchResult `dynamodbav:"matchResults,omitempty" json:"matchResults,omitempty"`
}

// GameDay is the item stored in tankmaze-gamedays.
type GameDay struct {
	GameDayID       string                   `dynamodbav:"gameDayId"                  json:"gameDayId"`
	Name            string                   `dynamodbav:"name,omitempty"             json:"name,omitempty"`
	Version         int                      `dynamodbav:"version"                    json:"version"`
	Schedule        GameDaySchedule          `dynamodbav:"schedule"                   json:"schedule"`
	Phases          GameDayPhases            `dynamodbav:"phases"                     json:"phases"`
	RegisteredTanks []MatchTank              `dynamodbav:"registeredTanks,omitempty"  json:"registeredTanks,omitempty"`
	Groups          []Group                  `dynamodbav:"groups,omitempty"           json:"groups,omitempty"`
	Bracket         map[string][]BracketSlot `dynamodbav:"bracket,omitempty"          json:"bracket,omitempty"`
	PlacementPoints map[string]int           `dynamodbav:"placementPoints,omitempty"  json:"placementPoints,omitempty"`
	CreatedAt       int64                    `dynamodbav:"createdAt"                  json:"createdAt"`
	Autofill        bool                     `dynamodbav:"autofill,omitempty"         json:"autofill,omitempty"`
	ForcedMapIDs    []string                 `dynamodbav:"forcedMapIds,omitempty"     json:"forcedMapIds,omitempty"`
	RandomMaps      bool                     `dynamodbav:"randomMaps,omitempty"       json:"randomMaps,omitempty"`
}

// Ranking is the item stored in tankmaze-rankings.
type Ranking struct {
	TankID    string `dynamodbav:"tankId"`
	GameDayID string `dynamodbav:"gameDayId"`
	Points    int    `dynamodbav:"points"`
	Placement int    `dynamodbav:"placement"`
	ExpiresAt int64  `dynamodbav:"expiresAt"`
	TTL       int64  `dynamodbav:"ttl"`
}

// ScoreTransferInput carries all data needed for the atomic score transfer
// operation. The caller must supply the source tank's existing rankings and
// the aggregate stats to be moved to the target.
type ScoreTransferInput struct {
	SourceTankID   string
	TargetTankID   string
	SourceRankings []Ranking
	GlobalScore    int
	BestFinish     *int // nil if source had no best finish
	GameDaysCount  int
	LastActiveAt   int64
}

// Map is the item stored in tankmaze-maps.
type Map struct {
	MapID       string   `dynamodbav:"mapId"       json:"mapId"`
	Slug        string   `dynamodbav:"slug"        json:"slug"`
	Name        string   `dynamodbav:"name"        json:"name"`
	Description string   `dynamodbav:"description" json:"description"`
	Layout      [][]bool `dynamodbav:"layout"      json:"layout"`
	IsBuiltIn   bool     `dynamodbav:"isBuiltIn"   json:"isBuiltIn"`
	IsActive    bool     `dynamodbav:"isActive"    json:"isActive"`
	CreatedAt   int64    `dynamodbav:"createdAt"   json:"createdAt"`
}
