package db

// Tank is the item stored in tankmaze-tanks.
type Tank struct {
	TankID               string `dynamodbav:"tankId"`
	UserID               string `dynamodbav:"userId"`
	Name                 string `dynamodbav:"name"`
	GlobalScore          int    `dynamodbav:"globalScore"`
	BestFinish           *int   `dynamodbav:"bestFinish"`       // nil = never placed
	GameDaysCount        int    `dynamodbav:"gameDaysCount"`
	LastActiveAt         int64  `dynamodbav:"lastActiveAt"`
	CreatedAt            int64  `dynamodbav:"createdAt"`
	ForkedFromTankID     string `dynamodbav:"forkedFromTankId,omitempty"`
	ForkedFromVersion    string `dynamodbav:"forkedFromVersion,omitempty"`
	ScoreTransferredTo   string `dynamodbav:"scoreTransferredTo,omitempty"`
	ScoreTransferredFrom string `dynamodbav:"scoreTransferredFrom,omitempty"`
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
	Speed       int `dynamodbav:"speed"`
	SensorRange int `dynamodbav:"sensorRange"`
	Damage      int `dynamodbav:"damage"`
	Armor       int `dynamodbav:"armor"`
	FireRate    int `dynamodbav:"fireRate"`
}

// TankVersion is the item stored in tankmaze-tank-versions.
type TankVersion struct {
	TankID               string        `dynamodbav:"tankId"`
	Version              string        `dynamodbav:"version"`
	VersionType          string        `dynamodbav:"versionType"` // "major" | "minor"
	Config               VersionConfig `dynamodbav:"config"`
	WasmS3Key            string        `dynamodbav:"wasmS3Key,omitempty"`
	SourceS3Key          string        `dynamodbav:"sourceS3Key,omitempty"`
	WasmSHA256           string        `dynamodbav:"wasmSha256,omitempty"`
	CompileStatus        string        `dynamodbav:"compileStatus"` // "pending"|"compiling"|"ready"|"failed"
	CompileError         string        `dynamodbav:"compileError,omitempty"`
	RegisteredForGameDay string        `dynamodbav:"registeredForGameDay,omitempty"`
	CreatedAt            int64         `dynamodbav:"createdAt"`
	// Major-only stats (omitted on minor versions)
	WinRate          float64 `dynamodbav:"winRate,omitempty"`
	MatchesPlayed    int     `dynamodbav:"matchesPlayed,omitempty"`
	AvgDamageDealt   float64 `dynamodbav:"avgDamageDealt,omitempty"`
	AvgSurvivalTicks float64 `dynamodbav:"avgSurvivalTicks,omitempty"`
	// Minor-only stats
	TestMatchCount int  `dynamodbav:"testMatchCount,omitempty"`
	Disqualified   bool `dynamodbav:"disqualified,omitempty"`
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
	TankID  string `dynamodbav:"tankId"`
	Version string `dynamodbav:"version"`
}

// MatchResult is the outcome map written when a match ends.
// Winner is nil when Reason is "both_lose".
type MatchResult struct {
	Winner       *int   `dynamodbav:"winner"`
	Reason       string `dynamodbav:"reason"`
	DamageA      int    `dynamodbav:"damageA"`
	DamageB      int    `dynamodbav:"damageB"`
	MovesA       int    `dynamodbav:"movesA"`
	MovesB       int    `dynamodbav:"movesB"`
	TicksElapsed int    `dynamodbav:"ticksElapsed"`
	Flawless     bool   `dynamodbav:"flawless"`
}

// Match is the item stored in tankmaze-matches.
type Match struct {
	MatchID      string       `dynamodbav:"matchId"`
	MatchType    string       `dynamodbav:"matchType"` // "ranked"|"test-ai"|"test-own"|"informal"
	GameDayID    string       `dynamodbav:"gameDayId,omitempty"`
	Status       string       `dynamodbav:"status"` // "scheduled"|"countdown"|"active"|"ended"
	MazeSeed     string       `dynamodbav:"mazeSeed,omitempty"`
	MapID        string       `dynamodbav:"mapId,omitempty"`
	TankA        MatchTank    `dynamodbav:"tankA"`
	TankB        MatchTank    `dynamodbav:"tankB"`
	TickLogS3Key string       `dynamodbav:"tickLogS3Key,omitempty"`
	Result       *MatchResult `dynamodbav:"result,omitempty"`
	CreatedAt    int64        `dynamodbav:"createdAt"`
	TTL          int64        `dynamodbav:"ttl,omitempty"`
}

// Connection is the item stored in tankmaze-connections.
// ReplayTick and ReplaySpeed are set by REPLAY_SEEK/REPLAY_SPEED and read by
// OBSERVE to know where and how fast to stream the replay.
type Connection struct {
	ConnectionID string `dynamodbav:"connectionId"`
	MatchID      string `dynamodbav:"matchId"`
	TTL          int64  `dynamodbav:"ttl"`
	ReplayTick   int    `dynamodbav:"replayTick,omitempty"`  // 0 = start of match
	ReplaySpeed  string `dynamodbav:"replaySpeed,omitempty"` // "" means "1" (real-time)
}

// PhaseStatus tracks the lifecycle of one Game Day tournament phase.
type PhaseStatus struct {
	Status    string `dynamodbav:"status"` // "upcoming"|"running"|"complete"
	StartedAt int64  `dynamodbav:"startedAt,omitempty"`
	EndedAt   int64  `dynamodbav:"endedAt,omitempty"`
}

// GameDayPhases holds the status of each phase.
// Elimination phases are keyed by round: "r1", "r2", …
type GameDayPhases struct {
	RoundRobin  PhaseStatus            `dynamodbav:"roundRobin"`
	Elimination map[string]PhaseStatus `dynamodbav:"elimination,omitempty"`
	Final       PhaseStatus            `dynamodbav:"final"`
}

// GameDaySchedule holds the cron expressions for each phase.
// Elimination holds one cron expression per round (r1, r2, …).
type GameDaySchedule struct {
	RegistrationClose string   `dynamodbav:"registrationClose"`
	RoundRobin        string   `dynamodbav:"roundRobin"`
	Elimination       []string `dynamodbav:"elimination,omitempty"`
	Final             string   `dynamodbav:"final"`
}

// BracketSlot is one tank's slot in the elimination bracket.
type BracketSlot struct {
	TankID  string `dynamodbav:"tankId"`
	Version string `dynamodbav:"version"`
	Status  string `dynamodbav:"status"` // "playing"|"won"|"lost"|"both_lose"|"bye"
}

// GroupStanding is one tank's record within a round-robin group.
type GroupStanding struct {
	TankID  string `dynamodbav:"tankId"`
	Version string `dynamodbav:"version"`
	Wins    int    `dynamodbav:"wins"`
	Losses  int    `dynamodbav:"losses"`
	Points  int    `dynamodbav:"points"`
}

// Group is one round-robin group within a Game Day.
type Group struct {
	GroupID   string          `dynamodbav:"groupId"`
	Tanks     []MatchTank     `dynamodbav:"tanks"`
	Standings []GroupStanding `dynamodbav:"standings,omitempty"`
}

// GameDay is the item stored in tankmaze-gamedays.
// Bracket is keyed by round ("r1", "r2", …); each value is the ordered list
// of slots for that round.
type GameDay struct {
	GameDayID       string                   `dynamodbav:"gameDayId"`
	Schedule        GameDaySchedule          `dynamodbav:"schedule"`
	Phases          GameDayPhases            `dynamodbav:"phases"`
	RegisteredTanks []MatchTank              `dynamodbav:"registeredTanks,omitempty"`
	Groups          []Group                  `dynamodbav:"groups,omitempty"`
	Bracket         map[string][]BracketSlot `dynamodbav:"bracket,omitempty"`
	PlacementPoints map[string]int           `dynamodbav:"placementPoints,omitempty"` // tankId → points
	CreatedAt       int64                    `dynamodbav:"createdAt"`
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
	MapID       string   `dynamodbav:"mapId"`
	Slug        string   `dynamodbav:"slug"`
	Name        string   `dynamodbav:"name"`
	Description string   `dynamodbav:"description"`
	Layout      [][]bool `dynamodbav:"layout"`
	IsBuiltIn   bool     `dynamodbav:"isBuiltIn"`
	IsActive    bool     `dynamodbav:"isActive"`
	CreatedAt   int64    `dynamodbav:"createdAt"`
}
