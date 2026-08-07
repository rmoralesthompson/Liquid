package ui

import "strings"

// Slugify turns a display name into a URL-safe slug — "Thunder Yaks" ->
// "thunder-yaks". Every linkable team runs through it so the tile, the
// standings rows, and the /team/:id route agree on one key per team.
func Slugify(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-':
			b.WriteByte('-')
		}
	}
	return b.String()
}

// TeamLineup is one league team's full lineup and matchup context, shown on the
// /team/:id page. Your own team carries no Players — its lineup is the live
// board rendered by the roster child — while every other team carries a static,
// seeded roster. All names, managers, and clubs are FICTIONAL.
type TeamLineup struct {
	Slug     string
	Name     string
	Manager  string
	Record   string
	Opponent string   // this week's opponent (team name)
	IsYou    bool     // your team renders the live roster instead of Players
	Players  []Player // the static roster (nil for your team)
	Total    string   // projected total across Players (empty for your team)
}

// TeamStore resolves a team's lineup by slug for the /team/:id page. It is a
// read-only, app-lifetime lookup built once at startup, so it is safe to share
// across requests (no per-request mutation).
type TeamStore struct {
	teams map[string]TeamLineup
}

// NewTeamStore builds the store from a slug-keyed map of lineups.
func NewTeamStore(teams map[string]TeamLineup) *TeamStore {
	return &TeamStore{teams: teams}
}

// Get returns the lineup for a slug and whether it was found.
func (s *TeamStore) Get(slug string) (TeamLineup, bool) {
	t, ok := s.teams[slug]
	return t, ok
}
