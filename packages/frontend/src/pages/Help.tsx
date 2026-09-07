import { useEffect } from 'react';
import { useLocation } from 'react-router-dom';
import Layout, { cardStyle } from '../components/Layout';
import { HelpSection } from '../components/HelpDrawer';

/**
 * Public help/guide page (follow-on to item 266) — a single place that
 * consolidates the same explanatory copy already shown piecemeal in the
 * per-page intro cards and "? Help" drawers, organized by topic. Linked
 * from the login page and from every Help drawer's "Full help guide" link.
 * Public route (no RequireAuth), same as Leaderboard/GameDayList/GameDay,
 * so it's reachable pre-login and still renders inside the normal Layout
 * (nav + ad slots) rather than the bare Legal.tsx-style shell.
 */

const TOPICS = [
  { id: 'tanks', label: 'Tanks & Programming' },
  { id: 'gamedays', label: 'Game Days' },
  { id: 'leaderboard', label: 'Leaderboard & Ranking' },
  { id: 'friends', label: 'Friends & Messaging' },
  { id: 'plans', label: 'Plans & Tiers' },
];

function Section({ id, title, children }: { id: string; title: string; children: React.ReactNode }) {
  return (
    <div id={id} style={{ ...cardStyle, marginBottom: 24, scrollMarginTop: 20 }}>
      <h2 style={{ margin: '0 0 16px', fontSize: 19, fontWeight: 700, color: '#e7f1f7' }}>{title}</h2>
      {children}
    </div>
  );
}

export default function Help() {
  const location = useLocation();

  // React Router doesn't auto-scroll to a hash on navigation (unlike a
  // plain same-page <a href="#id"> click, which the browser handles for
  // free) — so a Help drawer's "Full help guide" link landing here from
  // another page needs this to actually land on the right section.
  useEffect(() => {
    if (!location.hash) return;
    const el = document.getElementById(location.hash.slice(1));
    el?.scrollIntoView({ behavior: 'smooth', block: 'start' });
  }, [location.hash]);

  return (
    <Layout>
      <h1 style={{ margin: '0 0 20px', fontSize: 24, fontWeight: 700, color: '#e7f1f7' }}>
        Help &amp; Guide
      </h1>

      <nav style={{ ...cardStyle, marginBottom: 28, display: 'flex', gap: 18, flexWrap: 'wrap' }}>
        {TOPICS.map((t) => (
          <a key={t.id} href={`#${t.id}`} style={{ color: '#ffab6b', fontSize: 13, fontWeight: 600, textDecoration: 'none' }}>
            {t.label}
          </a>
        ))}
      </nav>

      <Section id="tanks" title="Tanks &amp; Programming">
        <HelpSection heading="Overview">
          TankMaze is a code-battle platform: you write a tank's AI as a Go program, compile it to
          WebAssembly, and submit it here. Test it freely against the built-in bots, or challenge
          another player's tank, then register it for a Game Day — a scheduled tournament where
          tanks compete head-to-head and results feed a global ranking.
        </HelpSection>
        <HelpSection heading="Writing a tank">
          Implement <code>Tick(sensors Sensors) Action</code> — the server calls it once per game
          tick, and it must return a single action. Package-level variables persist across ticks,
          so they're your tank's memory; there's no real-time control once a match starts, and no
          other state to manage.
        </HelpSection>
        <HelpSection heading="Stats">
          The Config panel allocates exactly 15 points across five stats (1–5 each): Speed, Sensor
          Range, Damage, Armor, and Fire Rate. Save &amp; Validate rejects anything that doesn't sum
          to 15 before it even tries to compile.
        </HelpSection>
        <HelpSection heading="Testing">
          <strong>Test vs. AI</strong> and <strong>Challenge</strong> run unranked matches on any
          version, any time — neither affects your ranked stats, so it's the safe way to iterate
          before promoting. Test vs. AI runs against a built-in bot (Scout, Bruiser, Ranger, or
          Randy); Challenge runs against another player's tank — it only appears on tanks you
          don't own, not your own, so open the tank's page from the Leaderboard, a Game Day, or a
          friend's profile and challenge it from there.
        </HelpSection>
        <HelpSection heading="Templates">
          The "Start from a template" row on your Dashboard lets you fork one of the built-in AI
          tanks (Scout, Bruiser, Ranger, Randy) as a starting point instead of writing from a blank
          file — click the ⓘ icon on any of them to see its stats before forking.
        </HelpSection>
        <HelpSection heading="Saving &amp; versions">
          Every Save &amp; Validate bumps a minor version (v0.1, v0.2, …) — safe to do as often as
          you like, since minors are test-only. <strong>Promote to Major</strong> turns the current
          minor into the next major version (v1, v2, …); only major versions can be registered for
          a Game Day and count toward ranked stats.
        </HelpSection>
      </Section>

      <Section id="gamedays" title="Game Days">
        <HelpSection heading="Overview">
          TankMaze matches only run during a scheduled <strong>Game Day</strong>: a two-phase
          tournament. Every registered tank first plays a round-robin group of roughly 8 tanks,
          then the top finishers move into a single-elimination bracket down to a champion.
          Register a tank ahead of time from its own page to enter the next one — results feed
          straight into the tank's spot on the Leaderboard.
        </HelpSection>
        <HelpSection heading="Round robin">
          Registered tanks are ranked by Global Score and split into groups of about 8, seeded so
          each group gets an even spread of stronger and weaker tanks. Every tank in a group plays
          every other tank once; a win is worth 1 point, a flawless win (no damage taken) 2 points.
        </HelpSection>
        <HelpSection heading="Elimination bracket">
          The top finishers from each group (everyone, in smaller fields) advance into a
          single-elimination bracket seeded best-vs-worst. Lose once and you're out — the last
          tank standing is the Game Day Champion.
        </HelpSection>
        <HelpSection heading="Registering a tank">
          Registration is explicit and per-version: open your tank's page and register its current
          major version before the window closes. Promoting a new major version later means
          re-registering it — the old registration doesn't carry over automatically.
        </HelpSection>
        <HelpSection heading="Status &amp; badges">
          <strong>Upcoming</strong> / <strong>active</strong> / <strong>complete</strong> reflect
          where a Game Day is in its schedule. A <strong>↻ Recurring</strong> badge marks an
          occurrence that belongs to a repeating series (weekly, monthly, or every N days) — each
          occurrence still runs and scores independently. <strong>STUCK</strong> means a scheduled
          phase never actually fired; it's a platform hiccup, not something on your end.
        </HelpSection>
        <HelpSection heading="Reading the bracket">
          Slot colors mark each match's outcome: green for a win, gray for a loss, amber while a
          match is in progress, and a dim slot for a bye (an automatic advance with no match
          played).
        </HelpSection>
      </Section>

      <Section id="leaderboard" title="Leaderboard &amp; Ranking">
        <HelpSection heading="Overview">
          Every tank's <strong>Global Score</strong> is the sum of placement points it has earned
          across Game Days — a stronger finish in a bigger field is worth more points, and old
          results eventually drop off, so the score reflects recent competitive form rather than
          one lucky run.
        </HelpSection>
        <HelpSection heading="How ranking works">
          At the end of each Game Day, every tank earns placement points based on its final
          standing and the size of the field: the champion earns points equal to the number of
          competitors, and each lower placement is worth roughly half of the one above it.
          A tank's Global Score is the running sum of those points across every Game Day it has
          played, regardless of which version of the tank competed.
        </HelpSection>
        <HelpSection heading="Reading the table">
          <strong>Score</strong> is the Global Score described above. <strong>Best</strong> is the
          highest placement the tank has ever achieved. <strong>Days</strong> counts Game Days
          participated in. Ties in score are broken by best finish, then by Game Days played.
        </HelpSection>
        <HelpSection heading="Last active bar">
          This is a local staleness indicator only — it fills up the longer it's been since the
          tank's last Game Day, turning amber and then red as a nudge that it may be time to
          compete again. It's unrelated to how long placement points stay valid for scoring.
        </HelpSection>
      </Section>

      <Section id="friends" title="Friends &amp; Messaging">
        <HelpSection heading="Overview">
          Friends is TankMaze's lightweight social layer, separate from Game Day competition. Add
          another Tank Author as a friend from their profile page — once they accept, you can send
          each other direct messages. It's built for coordinating test matches, swapping strategy,
          or just talking trash before your tanks meet in a bracket.
        </HelpSection>
        <HelpSection heading="Adding a friend">
          Friend requests are sent from a Tank Author's profile page. Once you send one it shows up
          as pending until the other person accepts or declines it.
        </HelpSection>
        <HelpSection heading="Requests">
          <strong>Friend requests</strong> are incoming requests waiting on your answer.
          <strong> Sent requests</strong> are ones you sent that are still pending — you can cancel
          those any time before they're answered.
        </HelpSection>
        <HelpSection heading="Messaging">
          Direct messages only work between accepted friends — it keeps the inbox to people you've
          actually chosen to connect with.
        </HelpSection>
      </Section>

      <Section id="plans" title="Plans &amp; Tiers">
        <HelpSection heading="Overview">
          TankMaze's tiers exist to cover the cost of compiling your tank's code, not to give
          anyone an edge in the maze — every tier plays by the exact same rules, stats, and
          scoring. Paying only gets you more tanks, more compilations per month, and an ad-free
          experience. Signed-in authors can see current pricing and their own usage from the
          Upgrade page, linked from the nav once you're signed in.
        </HelpSection>
        <HelpSection heading="Free">
          $0. Up to 2 registered tanks and 10 compilations per rolling 30-day window — enough to
          try TankMaze and compete in Game Days. Ads are shown on this tier.
        </HelpSection>
        <HelpSection heading="Builder">
          Pricing coming soon. Up to 5 registered tanks and 50 compilations per 30-day window, and
          no ads — aimed at authors iterating on multiple tanks and shipping more versions.
        </HelpSection>
        <HelpSection heading="Pro">
          Pricing coming soon. Up to 15 registered tanks and 200 compilations per 30-day window,
          and no ads — for running a full roster of strategies.
        </HelpSection>
      </Section>
    </Layout>
  );
}
