package brightness

import (
	"math"
	"testing"
	"time"
)

func approxEqual(a, b, relTol float64) bool {
	if a == b {
		return true
	}
	diff := math.Abs(a - b)
	maxAB := math.Max(math.Abs(a), math.Abs(b))
	if maxAB == 0 {
		return diff < 1e-9
	}
	return diff/maxAB <= relTol
}

func TestSunPositionRanges(t *testing.T) {
	loc := Location{Lat: 47.6062, Lon: -122.3321}
	t0 := time.Date(2025, 6, 21, 12, 0, 0, 0, time.UTC)
	az, zen, err := SunPosition(t0, loc)
	if err != nil {
		t.Fatalf("SunPosition error: %v", err)
	}
	if az < 0 || az >= 360 {
		t.Fatalf("az out of range: %v", az)
	}
	if zen < 0 || zen > 180 {
		t.Fatalf("zen out of range: %v", zen)
	}
}

func TestClearSkyDayNight(t *testing.T) {
	loc := Location{Lat: 0, Lon: 0}

	// Daytime at equator approximate (noon UTC)
	day := time.Date(2024, 6, 21, 12, 0, 0, 0, time.UTC)
	dni, _, ghi, err := ClearSkyIrradiance(day, loc)
	if err != nil {
		t.Fatalf("ClearSkyIrradiance error: %v", err)
	}
	if dni <= 0 || ghi <= 0 {
		t.Fatalf("expected positive irradiance at day, got dni=%.3f ghi=%.3f", dni, ghi)
	}

	// Nighttime at equator approximate (midnight UTC)
	night := time.Date(2024, 6, 21, 0, 0, 0, 0, time.UTC)
	dni2, _, ghi2, err := ClearSkyIrradiance(night, loc)
	if err != nil {
		t.Fatalf("ClearSkyIrradiance error: %v", err)
	}
	if dni2 != 0 || ghi2 != 0 {
		t.Fatalf("expected zero irradiance at night, got dni=%.3f ghi=%.3f", dni2, ghi2)
	}
}

func TestIlluminanceOnTiltHorizontalDirect(t *testing.T) {
	loc := Location{Lat: 37.7749, Lon: -122.4194}
	t0 := time.Date(2025, 6, 21, 19, 0, 0, 0, time.UTC) // around midday in PDT

	// compute sun position
	sunAz, sunZen, err := SunPosition(t0, loc)
	if err != nil {
		t.Fatalf("SunPosition error: %v", err)
	}
	if sunZen >= 90.0 {
		t.Skip("sun below horizon for chosen time; skip direct test")
	}

	dni, dhi, ghi, err := ClearSkyIrradiance(t0, loc)
	if err != nil {
		t.Fatalf("ClearSkyIrradiance error: %v", err)
	}

	// horizontal surface (beta=0) should give cosInc = cos(zenith)
	E_direct, _, _, _ := IlluminanceOnTilt(dni, dhi, ghi, sunAz, sunZen, 0.0, 0.0, 0.2, 120.0)

	cosZen := math.Cos(degToRad(sunZen))
	expected := dni * cosZen * 120.0
	if !approxEqual(E_direct, expected, 1e-6) {
		t.Fatalf("E_direct mismatch: got %.6f expected %.6f", E_direct, expected)
	}
}

func TestApplyCloudAttenuationMonotonic(t *testing.T) {
	loc := Location{Lat: 34.05, Lon: -118.25}
	t0 := time.Date(2025, 6, 21, 19, 0, 0, 0, time.UTC)
	dni, dhi, ghi, err := ClearSkyIrradiance(t0, loc)
	if err != nil {
		t.Fatalf("ClearSkyIrradiance error: %v", err)
	}

	_, _, ghi0 := dni, dhi, ghi
	_, _, ghi1 := ApplyCloudAttenuation(dni, dhi, ghi, 0.5)
	_, _, ghi2 := ApplyCloudAttenuation(dni, dhi, ghi, 1.0)
	if !(ghi0 >= ghi1 && ghi1 >= ghi2) {
		t.Fatalf("ghi not monotonic: ghi0=%.3f ghi1=%.3f ghi2=%.3f", ghi0, ghi1, ghi2)
	}
}
