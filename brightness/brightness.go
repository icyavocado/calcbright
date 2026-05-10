package brightness

import (
	"math"
	"time"
)

// Basic types
type Location struct {
	Lat       float64
	Lon       float64
	AltMeters float64
}

type Orientation struct {
	AzimuthDeg float64 // screen azimuth, degrees
	TiltDeg    float64 // screen tilt, degrees from horizontal
}

type DeviceSpec struct {
	DisplayNits     float64
	Reflectance     float64 // 0..1
	AntiGlareFactor float64 // multiplier 0..1
}

type Environment struct {
	CloudFraction float64
	GroundAlbedo  float64
	Turbidity     float64
}

type AnalyzeOptions struct {
	Time             time.Time
	Location         Location
	Orientation      Orientation
	Device           DeviceSpec
	Environment      Environment
	LuminousEfficacy float64
	OWMKey           string
	CacheTTL         time.Duration
	OWMTimeout       time.Duration
}

type Report struct {
	Time                   time.Time
	Location               Location
	Orientation            Orientation
	EDirect                float64
	EDiffuse               float64
	EReflected             float64
	ETotal                 float64
	ReflectedLuminance     float64
	PerceivedLuminance     float64
	ContrastRatio          float64
	RecommendedDisplayNits float64
	Glare                  bool
	DataSource             string
}

// Helper math
func degToRad(d float64) float64 { return d * math.Pi / 180.0 }
func radToDeg(r float64) float64 { return r * 180.0 / math.Pi }

// SunPosition returns solar azimuth and zenith angles in degrees.
// Azimuth: degrees clockwise from North (0..360). Zenith: 0=overhead, 90=horizon.
func SunPosition(t time.Time, loc Location) (azimuthDeg, zenithDeg float64, err error) {
	// Convert to UTC
	utc := t.UTC()

	year, month, day := utc.Date()
	hour := utc.Hour()
	minute := utc.Minute()
	second := utc.Second()

	// Julian Day
	yy := year
	m := int(month)
	D := float64(day) + (float64(hour) / 24.0) + (float64(minute) / 1440.0) + (float64(second) / 86400.0)
	if m <= 2 {
		yy -= 1
		m += 12
	}
	A := int(float64(yy) / 100.0)
	B := 2 - A + int(float64(A)/4.0)
	jd := math.Floor(365.25*float64(yy+4716)) + math.Floor(30.6001*float64(m+1)) + D + float64(B) - 1524.5
	T := (jd - 2451545.0) / 36525.0

	// Solar coordinates (NOAA algorithm)
	L0 := math.Mod(280.46646+36000.76983*T+0.0003032*T*T, 360.0)
	M := 357.52911 + 35999.05029*T - 0.0001537*T*T
	e := 0.016708634 - 0.000042037*T - 0.0000001267*T*T
	Mrad := degToRad(M)
	C := (1.914602-0.004817*T-0.000014*T*T)*math.Sin(Mrad) + (0.019993-0.000101*T)*math.Sin(2*Mrad) + 0.000289*math.Sin(3*Mrad)
	sunTrueLong := L0 + C
	omega := 125.04 - 1934.136*T
	lambda := sunTrueLong - 0.00569 - 0.00478*math.Sin(degToRad(omega))

	// Obliquity
	eps0 := 23.0 + (26.0 / 60.0) + (21.448 / 3600.0) - (46.8150*T+0.00059*T*T-0.001813*T*T*T)/3600.0
	eps := eps0 + 0.00256*math.Cos(degToRad(omega))

	// Declination
	lambdaRad := degToRad(lambda)
	epsRad := degToRad(eps)
	decl := math.Asin(math.Sin(epsRad) * math.Sin(lambdaRad))

	// Equation of time (minutes)
	yyTan := math.Tan(epsRad / 2.0)
	yyTan = yyTan * yyTan
	L0rad := degToRad(L0)
	eqTime := 4.0 * radToDeg(yyTan*math.Sin(2.0*L0rad)-2.0*e*math.Sin(Mrad)+4.0*e*yyTan*math.Sin(Mrad)*math.Cos(2.0*L0rad)-0.5*yyTan*yyTan*math.Sin(4.0*L0rad)-1.25*e*e*math.Sin(2.0*Mrad))

	// True solar time (minutes)
	minutes := float64(hour*60+minute) + float64(second)/60.0
	tst := math.Mod(minutes+eqTime+4.0*loc.Lon, 1440.0)
	hourAngle := tst/4.0 - 180.0
	if hourAngle < -180.0 {
		hourAngle += 360.0
	}
	// Convert to radians
	latRad := degToRad(loc.Lat)
	Hrad := degToRad(hourAngle)

	cosZenith := math.Sin(latRad)*math.Sin(decl) + math.Cos(latRad)*math.Cos(decl)*math.Cos(Hrad)
	if cosZenith > 1.0 {
		cosZenith = 1.0
	}
	if cosZenith < -1.0 {
		cosZenith = -1.0
	}
	zenith := radToDeg(math.Acos(cosZenith))

	// Azimuth
	// The ATAN2 formula produces azimuth measured from south (0 = south,
	// positive = west). Add 180° to convert to the clockwise-from-north
	// convention documented on SunPosition's return values.
	azRad := math.Atan2(math.Sin(Hrad), math.Cos(Hrad)*math.Sin(latRad)-math.Tan(decl)*math.Cos(latRad))
	azDeg := math.Mod(radToDeg(azRad)+180.0, 360.0)

	return azDeg, zenith, nil
}

// ClearSkyIrradiance returns approximate DNI, DHI, GHI (W/m^2) for given time+location.
// Simple empirical model; good-enough initial fallback when OWM radiation not present.
func ClearSkyIrradiance(t time.Time, loc Location) (dni, dhi, ghi float64, err error) {
	sunAz, sunZen, err := SunPosition(t, loc)
	_ = sunAz
	if err != nil {
		return 0, 0, 0, err
	}
	if sunZen >= 90.0 {
		return 0, 0, 0, nil
	}

	// Day of year
	n := float64(t.UTC().YearDay())
	I0 := 1367.0 * (1.0 + 0.033*math.Cos(2.0*math.Pi*n/365.0))

	cosZen := math.Cos(degToRad(sunZen))
	if cosZen <= 0 {
		return 0, 0, 0, nil
	}

	// Relative optical air mass (Kasten & Young)
	m := 1.0 / (cosZen + 0.50572*math.Pow(6.07995+(90.0-sunZen), -1.6364))

	// Simple transmittance model (turbidity effect aggregated)
	trans := math.Exp(-0.14 * m)

	dni = I0 * trans
	dhi = 0.1 * dni * cosZen
	ghi = dni*cosZen + dhi
	if dni < 0 {
		dni = 0
	}
	if dhi < 0 {
		dhi = 0
	}
	if ghi < 0 {
		ghi = 0
	}
	_ = sunZen
	return dni, dhi, ghi, nil
}

// ApplyCloudAttenuation applies empirical cloud attenuation factor to irradiance values.
func ApplyCloudAttenuation(dni, dhi, ghi float64, cloud float64) (dniA, dhiA, ghiA float64) {
	if cloud < 0 {
		cloud = 0
	}
	if cloud > 1 {
		cloud = 1
	}
	f := 1.0 - 0.75*math.Pow(cloud, 3.4)
	if f < 0 {
		f = 0
	}
	dniA = dni * f
	dhiA = dhi * f
	ghiA = ghi * f
	return
}

// IlluminanceOnTilt converts irradiance W/m^2 to illuminance (lux) on tilted surface.
func IlluminanceOnTilt(dni, dhi, ghi float64, sunAz, sunZen, surfAz, surfTilt, albedo, luminousEfficacy float64) (E_direct, E_diffuse, E_reflected, E_total float64) {
	// angles to radians
	beta := degToRad(surfTilt)
	sunZenRad := degToRad(sunZen)
	sunAzRad := degToRad(sunAz)
	surfAzRad := degToRad(surfAz)

	// incidence angle
	cosInc := math.Cos(sunZenRad)*math.Cos(beta) + math.Sin(sunZenRad)*math.Sin(beta)*math.Cos(sunAzRad-surfAzRad)
	if cosInc < 0 {
		cosInc = 0
	}

	E_direct = dni * cosInc * luminousEfficacy
	E_diffuse = dhi * (1.0 + math.Cos(beta)) / 2.0 * luminousEfficacy
	E_reflected = ghi * albedo * (1.0 - math.Cos(beta)) / 2.0 * luminousEfficacy
	E_total = E_direct + E_diffuse + E_reflected
	return
}

// ReflectedLuminance: Lambertian reflectance
func ReflectedLuminance(E_lux, reflectance float64) float64 {
	return (E_lux * reflectance) / math.Pi
}

func PerceivedLuminance(displayNits, reflected float64) float64 {
	return displayNits + reflected
}

// AnalyzeWithValues performs analysis given irradiance values (W/m^2) or falls back to ClearSky.
func AnalyzeWithValues(t time.Time, loc Location, orient Orientation, dev DeviceSpec, env Environment, luminousEfficacy float64, dni, dhi, ghi float64) (Report, error) {
	// If irradiance not provided (all zeros), compute clear-sky
	var err error
	if dni == 0 && dhi == 0 && ghi == 0 {
		dni, dhi, ghi, err = ClearSkyIrradiance(t, loc)
		if err != nil {
			return Report{}, err
		}
	}

	// Apply sensible defaults before calculations so they are used consistently.
	if dev.DisplayNits <= 0 {
		dev.DisplayNits = 300
	}
	if dev.Reflectance <= 0 {
		dev.Reflectance = 0.05
	}
	if env.GroundAlbedo == 0 {
		env.GroundAlbedo = 0.2
	}
	if luminousEfficacy <= 0 {
		luminousEfficacy = 120
	}

	sunAz, sunZen, sunErr := SunPosition(t, loc)
	if sunErr != nil {
		return Report{}, sunErr
	}

	E_direct, E_diffuse, E_reflected, E_total := IlluminanceOnTilt(dni, dhi, ghi, sunAz, sunZen, orient.AzimuthDeg, orient.TiltDeg, env.GroundAlbedo, luminousEfficacy)

	reflectedL := ReflectedLuminance(E_total, dev.Reflectance)
	perceived := PerceivedLuminance(dev.DisplayNits, reflectedL)

	contrast := 0.0
	if reflectedL <= 0 {
		contrast = math.Inf(1)
	} else {
		contrast = dev.DisplayNits / reflectedL
	}

	glare := false
	if reflectedL > dev.DisplayNits*0.2 || contrast < 3.0 {
		glare = true
	}

	report := Report{
		Time:                   t,
		Location:               loc,
		Orientation:            orient,
		EDirect:                E_direct,
		EDiffuse:               E_diffuse,
		EReflected:             E_reflected,
		ETotal:                 E_total,
		ReflectedLuminance:     reflectedL,
		PerceivedLuminance:     perceived,
		ContrastRatio:          contrast,
		RecommendedDisplayNits: perceived, // naive default: equal perceived
		Glare:                  glare,
		DataSource:             "model",
	}
	return report, nil
}

// Note: AnalyzeWithOWM implemented in owm.go
