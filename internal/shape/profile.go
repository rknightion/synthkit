// SPDX-License-Identifier: AGPL-3.0-only

package shape

// Profile is the declarative form of a business-hours load profile: a timezone plus the Curve
// parameters. Every field is optional, so a Profile is also an override: a consumer overlays it
// field by field on the profile it inherits (a series on its metric, a metric on its service, a
// service on its workload) and resolves the result with DefaultCurve for any field no layer set.
// A nil *Profile declares nothing, and the consumer's inherited curve and clock apply unchanged.
//
// The Profile holds no state and no location; a consumer resolves it into a Curve and a
// *time.Location once at build time.
type Profile struct {
	// Timezone is the IANA zone the hours are read in. Empty keeps the inherited zone.
	Timezone string `yaml:"timezone"`
	// Floor is the overnight load fraction of the plateau, in [0,1]. Default 0.1.
	Floor *float64 `yaml:"floor"`
	// RampUpStartHour is the local hour the morning ramp starts. Default 6.
	RampUpStartHour *float64 `yaml:"ramp_up_start_hour"`
	// PlateauStartHour is the local hour full load is reached. Default 10.
	PlateauStartHour *float64 `yaml:"plateau_start_hour"`
	// PlateauEndHour is the local hour the evening ramp starts. Default 16.
	PlateauEndHour *float64 `yaml:"plateau_end_hour"`
	// RampDownEndHour is the local hour the floor is reached again. Default 20.
	RampDownEndHour *float64 `yaml:"ramp_down_end_hour"`
	// WeekendFactor multiplies Saturday and Sunday load (>= 0). Omitted keeps the default of
	// the reading consumer.
	WeekendFactor *float64 `yaml:"weekend_factor"`
}
