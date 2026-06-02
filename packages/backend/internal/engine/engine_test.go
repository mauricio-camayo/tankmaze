package engine

import (
	"testing"

	tankmaze "github.com/tankmaze/sdk"
	"github.com/tankmaze/backend/internal/maze"
)

// openGrid returns a MazeGrid with only outer walls, useful for movement tests.
func openGrid() maze.MazeGrid {
	g := maze.NewGrid(maze.DefaultSize)
	for r := 0; r < g.Size; r++ {
		for c := 0; c < g.Size; c++ {
			g.Cells[r][c] = r > 0 && r < g.Size-1 && c > 0 && c < g.Size-1
		}
	}
	return g
}

func balancedCfg() tankmaze.TankConfig {
	return tankmaze.TankConfig{Name: "t", Speed: 3, SensorRange: 3, Damage: 3, Armor: 3, FireRate: 3}
}

func idle() tankmaze.Action  { return tankmaze.Action{Type: tankmaze.Idle} }
func nocrash() (bool, bool)  { return false, false }

func stepN(e *Engine, n int, a, b tankmaze.Action) *Result {
	for i := 0; i < n; i++ {
		if r := e.Step(a, b, false, false); r != nil {
			return r
		}
	}
	return nil
}

// ---- Movement ---------------------------------------------------------------

func TestMove_ForwardAdvancesPosition(t *testing.T) {
	g := openGrid()
	e := New(g, balancedCfg(), balancedCfg(), 200, 1, 0)
	startPos := e.tanks[0].pos

	act := tankmaze.Action{Type: tankmaze.Move, Direction: tankmaze.Forward}
	e.Step(act, idle(), false, false)

	d := dirDelta[tankmaze.S] // tank A faces South initially
	want := [2]int{startPos[0] + d[0], startPos[1] + d[1]}
	if e.tanks[0].pos != want {
		t.Errorf("forward move: got %v, want %v", e.tanks[0].pos, want)
	}
	if e.tanks[0].moveCount != 1 {
		t.Errorf("moveCount: got %d, want 1", e.tanks[0].moveCount)
	}
}

func TestMove_BackwardMovesOppositeToFacing(t *testing.T) {
	g := openGrid()
	e := New(g, balancedCfg(), balancedCfg(), 200, 1, 0)
	// Place tank A at row 3 facing South so backward (North) lands on row 2, which is open.
	e.tanks[0].pos = [2]int{3, 1}
	e.tanks[0].prevPos = e.tanks[0].pos

	act := tankmaze.Action{Type: tankmaze.Move, Direction: tankmaze.Backward}
	e.Step(act, idle(), false, false)

	want := [2]int{2, 1} // one step North of (3,1)
	if e.tanks[0].pos != want {
		t.Errorf("backward move: got %v, want %v", e.tanks[0].pos, want)
	}
}

func TestMove_BlockedByWall(t *testing.T) {
	// SpawnA is (1,1). Facing North, the next cell is (0,1) — the outer wall.
	g := openGrid()
	e := New(g, balancedCfg(), balancedCfg(), 200, 1, 0)
	e.tanks[0].facing = tankmaze.N // set directly; no rotation ambiguity

	moveAct := tankmaze.Action{Type: tankmaze.Move, Direction: tankmaze.Forward}
	e.Step(moveAct, idle(), false, false)

	if e.tanks[0].pos != g.SpawnA {
		t.Errorf("wall-blocked move should not change position; got %v", e.tanks[0].pos)
	}
	if e.tanks[0].moveCount != 0 {
		t.Errorf("blocked move should not count; moveCount=%d", e.tanks[0].moveCount)
	}
}

func TestMove_CooldownPreventsDuplicateMove(t *testing.T) {
	g := openGrid()
	// Speed=1 → cooldown=500ms → 5 ticks between moves.
	cfg := tankmaze.TankConfig{Name: "t", Speed: 1, SensorRange: 3, Damage: 3, Armor: 3, FireRate: 3}
	e := New(g, cfg, balancedCfg(), 200, 1, 0)

	act := tankmaze.Action{Type: tankmaze.Move, Direction: tankmaze.Forward}
	// Tick 0: moves successfully.
	e.Step(act, idle(), false, false)
	if e.tanks[0].moveCount != 1 {
		t.Fatalf("expected 1 move after tick 0")
	}

	// Ticks 1–4: cooldown active, moves rejected.
	for i := 0; i < 4; i++ {
		e.Step(act, idle(), false, false)
	}
	if e.tanks[0].moveCount != 1 {
		t.Errorf("cooldown: expected 1 move after 5 ticks, got %d", e.tanks[0].moveCount)
	}

	// Tick 5: cooldown expired, move accepted.
	e.Step(act, idle(), false, false)
	if e.tanks[0].moveCount != 2 {
		t.Errorf("after cooldown: expected 2 moves, got %d", e.tanks[0].moveCount)
	}
}

// ---- Rotation ---------------------------------------------------------------

func TestRotate_RightClockwise(t *testing.T) {
	g := openGrid()
	e := New(g, balancedCfg(), balancedCfg(), 200, 1, 0)

	// Tank A starts facing South.
	cases := []tankmaze.Direction{tankmaze.W, tankmaze.N, tankmaze.E, tankmaze.S} // S→W→N→E→S
	act := tankmaze.Action{Type: tankmaze.Rotate, Direction: tankmaze.Right}
	for _, want := range cases {
		e.Step(act, idle(), false, false)
		if e.tanks[0].facing != want {
			t.Errorf("after right turn: got %v, want %v", e.tanks[0].facing, want)
		}
	}
}

func TestRotate_LeftCounterClockwise(t *testing.T) {
	g := openGrid()
	e := New(g, balancedCfg(), balancedCfg(), 200, 1, 0)

	// Tank A starts facing South.
	cases := []tankmaze.Direction{tankmaze.E, tankmaze.N, tankmaze.W, tankmaze.S} // S→E→N→W→S
	act := tankmaze.Action{Type: tankmaze.Rotate, Direction: tankmaze.Left}
	for _, want := range cases {
		e.Step(act, idle(), false, false)
		if e.tanks[0].facing != want {
			t.Errorf("after left turn: got %v, want %v", e.tanks[0].facing, want)
		}
	}
}

// ---- Projectiles ------------------------------------------------------------

func TestFire_ProjectileCreated(t *testing.T) {
	g := openGrid()
	e := New(g, balancedCfg(), balancedCfg(), 200, 1, 0)
	fireAct := tankmaze.Action{Type: tankmaze.Fire}
	e.Step(fireAct, idle(), false, false)
	if len(e.projectiles) != 1 {
		t.Fatalf("expected 1 projectile, got %d", len(e.projectiles))
	}
	p := e.projectiles[0]
	if p.facing != tankmaze.S {
		t.Errorf("projectile facing: got %v, want S", p.facing)
	}
	if p.owner != 0 {
		t.Errorf("projectile owner: got %d, want 0", p.owner)
	}
}

func TestFire_ProjectileAdvancesNextTick(t *testing.T) {
	g := openGrid()
	e := New(g, balancedCfg(), balancedCfg(), 200, 1, 0)
	startPos := e.tanks[0].pos

	fireAct := tankmaze.Action{Type: tankmaze.Fire}
	e.Step(fireAct, idle(), false, false) // tick 0: fire, projectile at (1,1)

	if len(e.projectiles) == 0 {
		t.Fatal("no projectile after fire")
	}
	if e.projectiles[0].pos != startPos {
		t.Errorf("projectile should not move on fire tick; got %v, want %v", e.projectiles[0].pos, startPos)
	}

	e.Step(idle(), idle(), false, false) // tick 1: projectile moves 1 cell south (projSpeed=1)
	d := dirDelta[tankmaze.S]
	want := [2]int{startPos[0] + d[0], startPos[1] + d[1]}
	if len(e.projectiles) == 0 {
		t.Fatal("projectile vanished unexpectedly")
	}
	if e.projectiles[0].pos != want {
		t.Errorf("projectile position after 1 tick: got %v, want %v", e.projectiles[0].pos, want)
	}
}

func TestFire_ProjectileDestroyedByWall(t *testing.T) {
	// Tank A at (1,1) facing West — the cell at (1,0) is an outer wall.
	g := openGrid()
	e := New(g, balancedCfg(), balancedCfg(), 200, 1, 0)
	e.tanks[0].facing = tankmaze.W // face West; (1,0) is wall

	fireAct := tankmaze.Action{Type: tankmaze.Fire}
	e.Step(fireAct, idle(), false, false) // tick 0: fire at (1,1)
	e.Step(idle(), idle(), false, false)  // tick 1: projectile tries to enter (1,0) — wall
	if len(e.projectiles) != 0 {
		t.Errorf("expected projectile destroyed by wall; %d remaining", len(e.projectiles))
	}
}

func TestFire_ProjectileHitsTank(t *testing.T) {
	// Place tank B directly in front of tank A (tank A facing South at row 1,
	// place tank B at row 2, same col). Fire and advance.
	g := openGrid()
	e := New(g, balancedCfg(), balancedCfg(), 200, 1, 0)
	e.tanks[1].pos = [2]int{e.tanks[0].pos[0] + 2, e.tanks[0].pos[1]} // 2 rows south
	initialHP := e.tanks[1].hp

	fireAct := tankmaze.Action{Type: tankmaze.Fire}
	e.Step(fireAct, idle(), false, false) // tick 0: fire; projectile at SpawnA
	e.Step(idle(), idle(), false, false)  // tick 1: projectile moves to row+1
	e.Step(idle(), idle(), false, false)  // tick 2: projectile hits tank B at row+2

	expectedDmg := effectiveDamage(balancedCfg().Damage, balancedCfg().Armor)
	if e.tanks[1].hp != initialHP-expectedDmg {
		t.Errorf("tank B HP: got %d, want %d", e.tanks[1].hp, initialHP-expectedDmg)
	}
	if e.tanks[0].damageDealt != expectedDmg {
		t.Errorf("damageDealt: got %d, want %d", e.tanks[0].damageDealt, expectedDmg)
	}
	if len(e.projectiles) != 0 {
		t.Error("projectile should be destroyed after hitting tank")
	}
}

func TestFire_CooldownPreventsRapidFire(t *testing.T) {
	g := openGrid()
	// FireRate=1 → cooldown=2000ms → 20 ticks between shots.
	cfg := tankmaze.TankConfig{Name: "t", Speed: 3, SensorRange: 3, Damage: 3, Armor: 3, FireRate: 1}
	e := New(g, cfg, balancedCfg(), 200, 1, 0)

	fireAct := tankmaze.Action{Type: tankmaze.Fire}
	e.Step(fireAct, idle(), false, false) // tick 0: fires

	// Try firing again immediately.
	e.Step(fireAct, idle(), false, false)
	if len(e.projectiles) != 1 {
		t.Errorf("second fire during cooldown should be ignored; got %d projectiles", len(e.projectiles))
	}
}

// ---- Collision --------------------------------------------------------------

func TestCollision_BothTanksConverge(t *testing.T) {
	g := openGrid()
	e := New(g, balancedCfg(), balancedCfg(), 200, 1, 0)

	// Place tanks one cell apart so a forward move causes convergence.
	// A at (5,5) facing East, B at (5,6) facing West.
	e.tanks[0].pos = [2]int{5, 5}
	e.tanks[0].prevPos = e.tanks[0].pos
	e.tanks[0].facing = tankmaze.E
	e.tanks[1].pos = [2]int{5, 6}
	e.tanks[1].prevPos = e.tanks[1].pos
	e.tanks[1].facing = tankmaze.W

	moveAct := tankmaze.Action{Type: tankmaze.Move, Direction: tankmaze.Forward}
	e.Step(moveAct, moveAct, false, false)

	// Both pushed back.
	if e.tanks[0].pos != ([2]int{5, 5}) || e.tanks[1].pos != ([2]int{5, 6}) {
		t.Errorf("tanks not pushed back: A=%v B=%v", e.tanks[0].pos, e.tanks[1].pos)
	}
	if e.tanks[0].hp != 95 || e.tanks[1].hp != 95 {
		t.Errorf("collision damage: A.hp=%d B.hp=%d, both want 95", e.tanks[0].hp, e.tanks[1].hp)
	}
}

func TestCollision_SwapDetected(t *testing.T) {
	g := openGrid()
	e := New(g, balancedCfg(), balancedCfg(), 200, 1, 0)

	// A at (5,5) facing East, B at (5,6) facing West — but one step apart.
	// Wait — a swap means A moves to B's cell and B moves to A's cell.
	// Place A at (5,5), B at (5,6). A moves East, B moves West → swap.
	e.tanks[0].pos = [2]int{5, 5}
	e.tanks[0].prevPos = e.tanks[0].pos
	e.tanks[0].facing = tankmaze.E
	e.tanks[1].pos = [2]int{5, 6}
	e.tanks[1].prevPos = e.tanks[1].pos
	e.tanks[1].facing = tankmaze.W

	// Need both cooldowns at 0.
	e.tanks[0].moveCooldownMs = 0
	e.tanks[1].moveCooldownMs = 0

	moveAct := tankmaze.Action{Type: tankmaze.Move, Direction: tankmaze.Forward}
	e.Step(moveAct, moveAct, false, false)

	// Regardless of whether it's swap or converge, both should be pushed back.
	if e.tanks[0].hp != 95 || e.tanks[1].hp != 95 {
		t.Errorf("swap collision damage wrong: A=%d B=%d", e.tanks[0].hp, e.tanks[1].hp)
	}
}

// ---- Win conditions ---------------------------------------------------------

func TestWin_ByDestruction(t *testing.T) {
	g := openGrid()
	e := New(g, balancedCfg(), balancedCfg(), 200, 1, 0)

	// Set tank B HP to 1, position tank B one step ahead of a projectile that will hit.
	e.tanks[1].hp = 1
	e.tanks[1].pos = [2]int{e.tanks[0].pos[0] + 1, e.tanks[0].pos[1]}

	fireAct := tankmaze.Action{Type: tankmaze.Fire}
	e.Step(fireAct, idle(), false, false) // fire; projectile at SpawnA
	r := e.Step(idle(), idle(), false, false) // projectile hits B

	if r == nil {
		t.Fatal("expected result after tank B destroyed")
	}
	if r.Winner != 0 {
		t.Errorf("winner: got %d, want 0 (tank A)", r.Winner)
	}
	if r.Reason != ReasonDestroyed {
		t.Errorf("reason: got %v, want %v", r.Reason, ReasonDestroyed)
	}
}

func TestWin_Flawless(t *testing.T) {
	g := openGrid()
	e := New(g, balancedCfg(), balancedCfg(), 200, 1, 0)

	e.tanks[1].hp = 1
	e.tanks[1].pos = [2]int{e.tanks[0].pos[0] + 1, e.tanks[0].pos[1]}

	fireAct := tankmaze.Action{Type: tankmaze.Fire}
	e.Step(fireAct, idle(), false, false)
	r := e.Step(idle(), idle(), false, false)

	if r == nil || !r.Flawless {
		t.Errorf("expected flawless win; got %+v", r)
	}
}

func TestWin_NotFlawlessWhenDamageTaken(t *testing.T) {
	g := openGrid()
	e := New(g, balancedCfg(), balancedCfg(), 200, 1, 0)
	e.tanks[0].hp = 95 // simulate tank A took 5 damage

	e.tanks[1].hp = 1
	e.tanks[1].pos = [2]int{e.tanks[0].pos[0] + 1, e.tanks[0].pos[1]}

	e.Step(tankmaze.Action{Type: tankmaze.Fire}, idle(), false, false)
	r := e.Step(idle(), idle(), false, false)

	if r == nil || r.Flawless {
		t.Errorf("expected non-flawless win; got %+v", r)
	}
}

func TestWin_BothLoseFromProjectiles(t *testing.T) {
	g := openGrid()
	e := New(g, balancedCfg(), balancedCfg(), 200, 1, 0)

	// Both tanks at 1 HP, each has an in-flight projectile aimed at the other.
	e.tanks[0].hp = 1
	e.tanks[1].hp = 1

	// Manually inject two projectiles that will hit each tank this tick.
	dmg := effectiveDamage(3, 3)
	e.projectiles = []projectile{
		{pos: [2]int{e.tanks[1].pos[0], e.tanks[1].pos[1] + 1}, facing: tankmaze.W, owner: 0, damage: dmg},
		{pos: [2]int{e.tanks[0].pos[0], e.tanks[0].pos[1] - 1}, facing: tankmaze.E, owner: 1, damage: dmg},
	}

	r := e.Step(idle(), idle(), false, false)
	if r == nil {
		t.Fatal("expected result when both tanks killed simultaneously")
	}
	if r.Winner != -1 || r.Reason != ReasonBothLose {
		t.Errorf("expected both-lose; got winner=%d reason=%v", r.Winner, r.Reason)
	}
}

func TestWin_ByCrash(t *testing.T) {
	g := openGrid()
	e := New(g, balancedCfg(), balancedCfg(), 200, 1, 0)

	r := e.Step(idle(), idle(), true, false) // tank A crashes
	if r == nil || r.Winner != 1 || r.Reason != ReasonCodeCrash {
		t.Errorf("expected tank B wins on A crash; got %+v", r)
	}
}

func TestWin_BothCrash(t *testing.T) {
	g := openGrid()
	e := New(g, balancedCfg(), balancedCfg(), 200, 1, 0)

	r := e.Step(idle(), idle(), true, true)
	if r == nil || r.Winner != -1 || r.Reason != ReasonCodeCrash {
		t.Errorf("expected both-lose on double crash; got %+v", r)
	}
}

func TestWin_TickLimit_DamageTiebreak(t *testing.T) {
	g := openGrid()
	e := New(g, balancedCfg(), balancedCfg(), 1, 1, 0)

	e.tanks[0].damageDealt = 20
	e.tanks[1].damageDealt = 10

	r := e.Step(idle(), idle(), false, false)
	if r == nil || r.Winner != 0 || r.Reason != ReasonDamageTiebreak {
		t.Errorf("expected A wins damage tiebreak; got %+v", r)
	}
}

func TestWin_TickLimit_MovesTiebreak(t *testing.T) {
	g := openGrid()
	e := New(g, balancedCfg(), balancedCfg(), 1, 1, 0)

	e.tanks[0].moveCount = 5
	e.tanks[1].moveCount = 3

	r := e.Step(idle(), idle(), false, false)
	if r == nil || r.Winner != 0 || r.Reason != ReasonMovesTiebreak {
		t.Errorf("expected A wins moves tiebreak; got %+v", r)
	}
}

func TestWin_TickLimit_BothLose(t *testing.T) {
	g := openGrid()
	e := New(g, balancedCfg(), balancedCfg(), 1, 1, 0)

	r := e.Step(idle(), idle(), false, false)
	if r == nil || r.Winner != -1 || r.Reason != ReasonBothLose {
		t.Errorf("expected both-lose at tick limit; got %+v", r)
	}
}

// ---- Sensors ----------------------------------------------------------------

func TestSensors_WallDistances(t *testing.T) {
	g := openGrid()
	e := New(g, balancedCfg(), balancedCfg(), 200, 1, 0)

	// Tank A at (1,1) facing South, sensorRange=3 → maxRange=6.
	// North: outer wall at row 0 → distance 0.
	// West: outer wall at col 0 → distance 0.
	s := e.Sensors(0)
	if s.WallDistances[tankmaze.N] != 0 {
		t.Errorf("N wall distance: got %d, want 0", s.WallDistances[tankmaze.N])
	}
	if s.WallDistances[tankmaze.W] != 0 {
		t.Errorf("W wall distance: got %d, want 0", s.WallDistances[tankmaze.W])
	}
	// East: open cells from col 2 to at least col 7 (capped at 6).
	if s.WallDistances[tankmaze.E] != 6 {
		t.Errorf("E wall distance: got %d, want 6", s.WallDistances[tankmaze.E])
	}
}

func TestSensors_ProximityAndBearing(t *testing.T) {
	g := openGrid()
	e := New(g, balancedCfg(), balancedCfg(), 200, 1, 0)

	// Place tank B 2 cells south of tank A — well within sensorRange*2=6.
	e.tanks[1].pos = [2]int{e.tanks[0].pos[0] + 2, e.tanks[0].pos[1]}

	s := e.Sensors(0)
	if !s.ProximityAlert {
		t.Error("expected ProximityAlert true when opponent is close")
	}
	if s.OpponentBearing == nil || *s.OpponentBearing != tankmaze.BearingS {
		t.Errorf("expected bearing South; got %v", s.OpponentBearing)
	}
}

func TestSensors_NoProximityWhenFar(t *testing.T) {
	g := openGrid()
	e := New(g, balancedCfg(), balancedCfg(), 200, 1, 0)
	// Default positions: SpawnA=(1,1), SpawnB=(23,23) — far apart.
	s := e.Sensors(0)
	if s.ProximityAlert {
		t.Error("expected ProximityAlert false when opponent is far")
	}
	if s.OpponentBearing != nil {
		t.Error("expected nil OpponentBearing when out of range")
	}
}

// ---- Effective damage -------------------------------------------------------

func TestEffectiveDamage(t *testing.T) {
	cases := []struct {
		dmg, armor int
		want       int
	}{
		{3, 3, int(float64(30) * 0.7)}, // 30 * 0.7 = 21
		{5, 5, 25},                      // 50 * 0.5 = 25
		{1, 1, 9},                       // 10 * 0.9 = 9
		{5, 1, 45},                      // 50 * 0.9 = 45
	}
	for _, c := range cases {
		got := effectiveDamage(c.dmg, c.armor)
		if got != c.want {
			t.Errorf("effectiveDamage(%d, %d) = %d, want %d", c.dmg, c.armor, got, c.want)
		}
	}
}

// ---- Bearing ----------------------------------------------------------------

func TestCalcBearing(t *testing.T) {
	origin := [2]int{12, 12}
	cases := []struct {
		to      [2]int
		bearing tankmaze.Bearing
	}{
		{[2]int{10, 12}, tankmaze.BearingN},
		{[2]int{14, 12}, tankmaze.BearingS},
		{[2]int{12, 14}, tankmaze.BearingE},
		{[2]int{12, 10}, tankmaze.BearingW},
		{[2]int{10, 14}, tankmaze.BearingNE},
		{[2]int{14, 14}, tankmaze.BearingSE},
		{[2]int{14, 10}, tankmaze.BearingSW},
		{[2]int{10, 10}, tankmaze.BearingNW},
	}
	for _, c := range cases {
		got := calcBearing(origin, c.to)
		if got != c.bearing {
			t.Errorf("calcBearing(%v, %v) = %v, want %v", origin, c.to, got, c.bearing)
		}
	}
}
