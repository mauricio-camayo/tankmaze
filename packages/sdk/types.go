package tankmaze

// Direction is a cardinal heading used for movement, rotation, and wall distance sensing.
type Direction int

const (
	N Direction = iota
	S
	E
	W
)

// Bearing is an 8-compass direction used only for OpponentBearing in Sensors.
type Bearing int

const (
	BearingN  Bearing = iota
	BearingNE
	BearingE
	BearingSE
	BearingS
	BearingSW
	BearingW
	BearingNW
)

// Point is a cell coordinate on the maze grid. (0,0) is the top-left corner.
type Point struct {
	X int
	Y int
}

// TankConfig declares the tank's name and stat allocation.
// Speed + SensorRange + Damage + Armor + FireRate must equal exactly 15.
// Each stat must be between 1 and 5 inclusive.
type TankConfig struct {
	Name        string
	Speed       int
	SensorRange int
	Damage      int
	Armor       int
	FireRate    int
}

// Sensors is passed to Tick every game tick. It contains only what the tank's
// hardware can detect — the full maze layout is never exposed.
type Sensors struct {
	Facing         Direction
	Position       Point
	HP             int
	WallDistances  map[Direction]int // cells to nearest wall in each direction, capped at SensorRange×2
	ProximityAlert bool              // true when opponent is within sensor range
	OpponentBearing *Bearing         // 8-compass direction to opponent; nil if not in range
	MoveCooldown   int              // milliseconds until next move is allowed (0 = ready)
	FireCooldown   int              // milliseconds until next shot is allowed (0 = ready)
	Tick           int              // monotonically increasing tick counter
}

// ActionType identifies what a tank intends to do this tick.
type ActionType int

const (
	Idle ActionType = iota
	Move
	Rotate
	Fire
	Scan
)

// MoveDirection qualifies Move and Rotate actions.
type MoveDirection int

const (
	Forward  MoveDirection = iota
	Backward
	Left
	Right
)

// Action is the single value returned by Tick. An invalid or zero-value Action
// is treated as Idle.
type Action struct {
	Type      ActionType
	Direction MoveDirection
}
