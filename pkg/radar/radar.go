// package radar implements mid-level logic for Ground-Controlled Interception (GCI)
package radar

import (
	"context"
	"sync"
	"time"

	"github.com/dharmab/skyeye/pkg/bearings"
	"github.com/dharmab/skyeye/pkg/brevity"
	"github.com/dharmab/skyeye/pkg/coalitions"
	"github.com/dharmab/skyeye/pkg/encyclopedia"
	"github.com/dharmab/skyeye/pkg/sim"
	"github.com/dharmab/skyeye/pkg/spatial"
	"github.com/dharmab/skyeye/pkg/trackfiles"
	"github.com/martinlindhe/unit"
	"github.com/paulmach/orb"
	"github.com/rs/zerolog/log"
)

// Radar consumes updates from the simulation, keeps track of each aircraft as a trackfile, and provides functions to collect the aircraft into groups.
type Radar interface {
	// SetBullseye updates the bullseye point for the given coalition.
	// The bullseye point is the reference point for polar coordinates provided in [Group.Bullseye].
	SetBullseye(orb.Point, coalitions.Coalition)
	// Bullseye returns the bullseye point for the given coalition.
	Bullseye(coalitions.Coalition) orb.Point
	// SetMissionTime updates the mission time. The mission time is used for computing magnetic declination.
	SetMissionTime(time.Time)
	// Declination returns the magnetic declination at the given point, at the time provided in SetMissionTime.
	Declination(orb.Point) unit.Angle
	// Run consumes updates from the simulation channels until the context is cancelled.
	Run(context.Context, *sync.WaitGroup)
	// FindCallsign returns the trackfile on the given coalition that mosty closely matches the given callsign,
	// or nil if no closely matching trackfile was found.
	// The first return value is the callsign of the trackfile, and the second is the trackfile itself.
	// The returned callsign may differ from the input callsign!
	FindCallsign(string, coalitions.Coalition) (string, *trackfiles.Trackfile)
	// FindUnit returns the trackfile for the given unit ID, or nil if no trackfile was found.
	FindUnit(uint64) *trackfiles.Trackfile
	// GetPicture returns a picture of the radar scope anchored at the center point, within the given radius,
	// filtered by the given coalition and contact category. The first return value is the total number of groups
	// and the second is a slice of up to to 3 high priority groups. Each group has Bullseye set relative to the
	// the point provided in SetBullseye.
	GetPicture(
		radius unit.Length,
		coalition coalitions.Coalition,
		category brevity.ContactCategory,
	) (int, []brevity.Group)
	// FindNearbyGroupsWithBRAA returns all groups within the given radius of the given point of interest, within the given
	// altitude block, filtered by the given coalition and contact category. Any given unit IDs are excluded from the search.
	// Each group has BRAA set relative to the given origin. The groups are ordered by increasing distance from the point
	// of interest.
	FindNearbyGroupsWithBRAA(
		origin,
		pointOfInterest orb.Point,
		minAltitude,
		maxAltitude,
		radius unit.Length,
		coalition coalitions.Coalition,
		category brevity.ContactCategory,
		excludedIDs []uint64,
	) []brevity.Group
	// FindNearbyGroupsWithBullseye returns all groups within the given radius of the given point of interest, within the given
	// altitude block, filtered by the given coalition and contact category. Any given unit IDs are excluded from the search.
	// Each group has Bullseye set relative to the point provided in SetBullseye. The groups are ordered by increasing distance
	// from the point of interest.
	FindNearbyGroupsWithBullseye(
		pointOfInterest orb.Point,
		minAltitude,
		maxAltitude,
		radius unit.Length,
		coalition coalitions.Coalition,
		category brevity.ContactCategory,
		excludedIDs []uint64,
	) []brevity.Group
	// FindNearestGroupWithBRAA returns the nearest group to the given origin (up to the given radius), within the
	// given altitude block, filtered by the given coalition and contact category. The group has BRAA set relative to
	// the given origin. Returns nil if no group was found.
	FindNearestGroupWithBRAA(
		origin orb.Point,
		minAltitude,
		maxAltitude,
		radius unit.Length,
		coalition coalitions.Coalition,
		category brevity.ContactCategory,
	) brevity.Group
	// FindNearestGroupWithBullseye returns the nearest group to the given point of interest (up to the given radius),
	// within the given altitude block, filtered by the given coalition and contact category. The group has Bullseye
	// set relative to the point provided in SetBullseye. Returns nil if no group was found.
	FindNearestGroupWithBullseye(
		pointOfIntest orb.Point,
		minAltitude,
		maxAltitude,
		radius unit.Length,
		coalition coalitions.Coalition,
		category brevity.ContactCategory,
	) brevity.Group
	// FindNearestGroupInSector returns the nearest group to the given origin (up to the given distance), within a 2D
	// circular sector defined by the given origin ,radius, bearing and arc, within the given altitude block, filtered
	// by the given coalition and contact category. The group has BRAA set relative to the given origin. Returns nil if
	// no group was found.
	FindNearestGroupInSector(
		origin orb.Point,
		minAltitude,
		maxAltitude,
		radius unit.Length,
		bearing bearings.Bearing,
		arc unit.Angle,
		coalition coalitions.Coalition,
		category brevity.ContactCategory,
	) brevity.Group
	// SetFadedCallback sets the callback function to be called when a trackfile fades.
	SetFadedCallback(FadedCallback)
	// SetRemovedCallback sets the callback function to be called when a trackfile is aged out.
	SetRemovedCallback(RemovedCallback)
	// Threats returns a map of threat groups of the given coalition to threatened object IDs.
	Threats(coalitions.Coalition) map[brevity.Group][]uint64
	// Merges returns a map of hostile groups of the given coalition to friendly trackfiles.
	Merges(coalitions.Coalition) map[brevity.Group][]*trackfiles.Trackfile
}

var _ Radar = &scope{}

type scope struct {
	starts                <-chan sim.Started
	updates               <-chan sim.Updated
	fades                 <-chan sim.Faded
	missionTime           time.Time
	bullseyes             sync.Map
	contacts              contactDatabase
	fadedCallback         FadedCallback
	removalCallback       RemovedCallback
	center                orb.Point
	mandatoryThreatRadius unit.Length
}

func New(coalition coalitions.Coalition, starts <-chan sim.Started, updates <-chan sim.Updated, fades <-chan sim.Faded, mandatoryThreatRadius unit.Length) Radar {
	return &scope{
		starts:                starts,
		updates:               updates,
		fades:                 fades,
		contacts:              newContactDatabase(),
		mandatoryThreatRadius: mandatoryThreatRadius,
	}
}

func (s *scope) SetMissionTime(t time.Time) {
	s.missionTime = t
}

func (s *scope) SetBullseye(bullseye orb.Point, coalition coalitions.Coalition) {
	current := s.Bullseye(coalition)
	if current.Lon() != bullseye.Lon() || current.Lat() != bullseye.Lat() {
		log.Info().
			Int("coalitionID", int(coalition)).
			Float64("lon", bullseye.Lon()).
			Float64("lat", bullseye.Lat()).
			Msg("updating bullseye")
	}
	s.bullseyes.Store(coalition, bullseye)
}

func (s *scope) Bullseye(coalition coalitions.Coalition) orb.Point {
	p, ok := s.bullseyes.Load(coalition)
	if !ok {
		return orb.Point{}
	}
	return p.(orb.Point)
}

// Run implements [Radar.Run].
func (s *scope) Run(ctx context.Context, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.collectFaded(ctx)
	}()

	s.updateCenterPoint()

	gcTicker := time.NewTicker(1 * time.Minute)
	defer gcTicker.Stop()
	recenterTicker := time.NewTicker(5 * time.Second)
	defer recenterTicker.Stop()
	for {
		select {
		case start := <-s.starts:
			log.Info().Time("missionTime", start.MissionTimestamp).Msg("clearing all trackfiles due to mission (re)start")
			s.contacts.reset()
		case update := <-s.updates:
			s.handleUpdate(update)
		case <-gcTicker.C:
			s.handleGarbageCollection()
		case <-recenterTicker.C:
			s.updateCenterPoint()
		case <-ctx.Done():
			return
		}
	}
}

// handleUpdate updates the database using the provided update.
func (s *scope) handleUpdate(update sim.Updated) {
	logger := log.With().
		Str("name", update.Labels.Name).
		Str("aircraft", update.Labels.ACMIName).
		Uint64("id", update.Labels.ID).
		Logger()

	trackfile, ok := s.contacts.getByID(update.Labels.ID)
	if ok {
		trackfile.Update(update.Frame)
	} else {
		trackfile = trackfiles.NewTrackfile(update.Labels)
		s.contacts.set(trackfile)
		logger.Info().Msg("created new trackfile")
	}
}

// handleGarbageCollection removes trackfiles that have not been updated in a long time.
func (s *scope) handleGarbageCollection() {
	for trackfile := range s.contacts.values() {
		logger := log.With().
			Uint64("id", trackfile.Contact.ID).
			Str("name", trackfile.Contact.Name).
			Str("aircraft", trackfile.Contact.ACMIName).
			Logger()

		lastSeen := trackfile.LastKnown().Time
		isOld := lastSeen.Before(s.missionTime.Add(-1 * time.Minute))
		isNotZero := !lastSeen.IsZero()
		if isNotZero && isOld {
			s.contacts.delete(trackfile.Contact.ID)
			logger.Info().
				Stringer("age", s.missionTime.Sub(lastSeen)).
				Msg("removed aged out trackfile")
		}
	}
}

// isValidTrack checks if the trackfile is valid. This means the following conditions are met:
//   - Last known position is not (0, 0)
//   - Speed is above 50 knots
func isValidTrack(trackfile *trackfiles.Trackfile) bool {
	isValidPosition := !spatial.IsZero(trackfile.LastKnown().Point)
	isAboveSpeedFilter := trackfile.Speed() > 50*unit.Knot
	isValid := isValidPosition && isAboveSpeedFilter
	return isValid
}

// isMatch checks:
//   - if the trackfile is of the given coalition
//   - if the trackfile is of the given contact category (or if the aircraft is not in the encyclopedia)
//   - if the trackfile is valid
func (s *scope) isMatch(trackfile *trackfiles.Trackfile, coalition coalitions.Coalition, filter brevity.ContactCategory) bool {
	if trackfile.Contact.Coalition != coalition {
		return false
	}
	if !isValidTrack(trackfile) {
		return false
	}
	data, ok := encyclopedia.GetAircraftData(trackfile.Contact.ACMIName)
	// If the aircraft is not in the encyclopedia, assume it matches
	matchesFilter := !ok || data.Category() == filter || filter == brevity.Aircraft
	return matchesFilter
}

func (s *scope) Declination(p orb.Point) unit.Angle {
	declination, err := bearings.Declination(p, s.missionTime)
	if err != nil {
		log.Error().Err(err).Msg("failed to get declination")
	}
	return declination
}
