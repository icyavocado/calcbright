Project: calculate-brightness

Overview
- Purpose: emulate ambient light sensor for laptops that lack hardware sensor. Primary data source: OpenWeatherMap. Input: location (lat, lon). System fetches weather/solar data, computes illuminance on screen, reflected luminance, and suggests display brightness (nits). Use cases: auto-brightness, glare warnings, accessibility checks, batch analysis.
- Language: Go

Goals
1. Estimate illuminance incident on laptop screen (lux) and resulting reflected luminance (cd/m^2).
2. Provide perceived screen luminance, contrast metric, glare flag, and recommended display nits.
3. Minimal CLI that asks only for location (lat, lon) by default.
4. Library API callable from other programs: AnalyzeWithOWM(ctx, lat, lon, opts).
5. CI: tests, lint, build matrix across Go versions.

Non-Goals
- Replace physical sensor measurements or provide certified photometry. Not real-time OS integration (initially).

User Stories
1. As user, run `calcbright --lat 47.6 --lon -122.3` and get recommended brightness, glare warning, and short summary. CLI asks only for location; device/orientation optional overrides.
2. As developer, call `AnalyzeWithOWM(ctx, lat, lon, opts)` to retrieve report using OpenWeatherMap data and local fallbacks.
3. As QA, run batch of locations/times to verify auto-brightness policy across environments.

Requirements

Functional
- Required input: latitude and longitude. CLI makes rest of decisions via OWM data.
- Optional inputs: time (defaults to now), altitude, device overrides (display nits, reflectance), orientation (tilt/azimuth) if user wants non-default.
 - OpenWeatherMap integration: use the Current Weather API `/data/2.5/weather` as the single external data source. Extract `dt`, `sunrise`, `sunset`, `clouds`, `uvi` (when present), `visibility`, and other ancillary fields. Solar irradiance fields are not used; irradiance is synthesized via the clear-sky model and attenuated by cloud fraction.
- Fallback: if OWM irradiance missing, compute clear-sky irradiance via sun position + simple clear-sky model, then attenuate by cloud fraction from OWM.

Models & formulas

- Sun position: compute solar azimuth and zenith using NOAA/NREL algorithm. Return azimuthDeg (clockwise from North) and zenithDeg (0° overhead, 90° horizon).
- Clear-sky irradiance: compute DNI/DHI/GHI via simple clear-sky model when OWM irradiance missing.
- Cloud attenuation: apply cloud fraction c = (`current.clouds` / 100). Default empirical mapping: f(c) = 1 - 0.75*c^3.4 (configurable).
- Luminous efficacy: default 120 lm/W. Optionally tune using `current.uvi` and cloud fraction.

Orientation conventions

- Surface tilt (beta): angle between surface and horizontal plane, degrees. 0° = horizontal facing sky, 90° = vertical, 180° = horizontal facing ground.
- Surface azimuth (surfAz): degrees clockwise from North. 0° = North, 90° = East, 180° = South.
- Sun azimuth uses same reference (clockwise from North). Document in API and CLI help.

Incidence math (direct beam)

- Use solar zenith z (degrees) and convert angles to radians. Incidence angle theta between sun rays and surface normal computed as:

  cos(theta) = cos(z) * cos(beta) + sin(z) * sin(beta) * cos(sunAz - surfAz)

- Clamp cos(theta) >= 0. Then E_direct = DNI * cos(theta) * luminousEfficacy.

Illuminance (tilted surface)

- E_direct = max(0, DNI * cos(theta)) * luminousEfficacy
- E_diffuse = DHI * (1 + cos(beta))/2 * luminousEfficacy  (isotropic diffuse approximation)
- E_reflected = GHI * ground_albedo * (1 - cos(beta))/2 * luminousEfficacy
- E_total = E_direct + E_diffuse + E_reflected  (lux)

- Reflected luminance: L_reflect = (E_total * rho) / pi  (cd/m^2)
- Perceived luminance: L_perceived = L_display + L_reflect  (nits == cd/m^2)
- Visibility: contrast ratio = L_display / max(L_reflect, epsilon). Default targetContrast = 3.0
- Glare: flag true if L_reflect > L_display * 0.2 or contrast < targetContrast.

OpenWeatherMap integration

  - Endpoints & fields used:
    - Current Weather API `/data/2.5/weather`: use `dt`, `sunrise`, `sunset`, `clouds` (0..100), `uvi` (if present), `visibility`, and `weather`.

  - Client behavior:
    1. Call Current Weather API `/data/2.5/weather?lat={lat}&lon={lon}&appid={key}&units=metric`.
    2. Extract `clouds`, `dt`, `sunrise`, `sunset`, `uvi` (if present) from the response.
    3. Compute sun position + clear-sky irradiance and attenuate by cloud fraction to synthesize DNI/DHI/GHI.
    4. Convert irradiance (W/m^2) → illuminance (lux) using luminous efficacy.

  - Reliability & limits:
    - HTTP timeout: 5s per request.
    - Retries: exponential backoff for network errors and 5xx, max 3 attempts. Do not retry on 4xx (401/403/429).
    - Rate-limit (429): surface clear error recommending caching and longer TTL; do not auto-spam retries.

  - Caching:
    - In-memory TTL cache keyed by `lat,lon,rounded_time_window` (default round to 10 minutes).
    - Default TTL: 10 minutes; configurable via env `OWM_CACHE_TTL` or client option.

- Configuration:
  - API key via `--owm-api-key` or env `OPENWEATHERMAP_API_KEY`.
  - Client options: base URL (override for testing), HTTP timeout, retries, cache TTL.

Notes on .env

- `.env` supported for local testing only. CLI will not auto-load `.env`. For local test runs, developer may source `.env` before running tests. Production usage should export `OPENWEATHERMAP_API_KEY` or pass `--owm-api-key`.

- Testability:
  - Support HTTP client injection or base URL override so tests can replay fixture JSON.
  - Provide fixture responses: clear-sky, heavy overcast, solar-radiation present.

Data mapping & conversions

  - OWM JSON -> model mapping:
    - `clouds.all` (int 0..100) -> cloudFraction = clouds.all / 100.0
    - `uvi` -> uvi (float, unitless) if present in the response.
    - `dt` -> timestamp (UTC) used for SunPosition input.
    - `sys.sunrise`, `sys.sunset` -> epoch seconds for sunrise/sunset.
    - `visibility` -> meters (optional).

 - Units & conversions:
   - Assume irradiance values reported in W/m^2. Use `units=metric` for consistency on other numeric fields (temperature).
   - Convert irradiance (W/m^2) -> illuminance (lux) via luminous efficacy (lm/W): lux = W/m^2 * lm/W.

Failure modes & defaults

 - Missing or partial OWM data:
   - If `current.clouds` missing, default cloudFraction = 0.5 (configurable).
   - If `current.uvi` missing, use default luminous efficacy 120 lm/W.
   - If irradiance fields missing, compute clear-sky + attenuation.
 - Sun below horizon:
   - If solar zenith > 90°, set direct beam contribution to zero.
 - API errors:
   - 401/403 -> ErrMissingAPIKey / ErrInvalidAPIKey.
   - 429 -> ErrRateLimited; recommend increasing cache TTL and reducing call frequency.
   - 5xx / network errors -> transient; retry with backoff up to configured attempts.

API Design (Library)

Package: brightness

Core types:

- type Location struct { Lat, Lon float64; AltMeters float64 }
- type Orientation struct { AzimuthDeg, TiltDeg float64 }
- type DeviceSpec struct { DisplayNits float64; Reflectance float64; AntiGlareFactor float64 }
- type Environment struct { CloudFraction float64; GroundAlbedo float64; Turbidity float64 }
- type AnalyzeOptions struct { Time time.Time; Orientation Orientation; Device DeviceSpec; Environment Environment; LuminousEfficacy float64; OWMKey string; CacheTTL time.Duration }

Interface & functions:

- type OWMClient interface {
  GetCurrent(ctx context.Context, lat, lon float64) (OWMCurrent, error)
}

- func AnalyzeWithOWM(ctx context.Context, lat, lon float64, opts AnalyzeOptions) (Report, error)
  - Behavior: fetch OWM data via OWMClient; if irradiance present use it; otherwise compute clear-sky + attenuation; compute E_total, L_reflect, L_perceived, contrast, glare flag.

- Error types: ErrMissingAPIKey, ErrRateLimited, ErrOWMUnavailable.

Report (output schema):

- type Report struct {
  Time time.Time
  Location Location
  Orientation Orientation
  EDirect float64
  EDiffuse float64
  EReflected float64
  ETotal float64
  ReflectedLuminance float64
  PerceivedLuminance float64
  ContrastRatio float64
  RecommendedDisplayNits float64
  Glare bool
  DataSource string // "owm-radiation" | "owm-clouds+model"
}

CLI Design

- Command: `calcbright`
- Minimal required flags: `--lat`, `--lon`.
- Optional flags: `--time` (RFC3339, default now), `--alt`, `--az`, `--tilt`, `--display-nits`, `--reflectance`, `--format` (text|json|csv), `--owm-api-key`.
- Default behavior: call AnalyzeWithOWM with OWM key from flag or env; print text summary by default.

Examples
- `calcbright --lat 47.6 --lon -122.3`
- `calcbright --lat 51.5 --lon -0.1 --format json --owm-api-key $OPENWEATHERMAP_API_KEY`

Testing

- Unit tests:
  - SunPosition vs reference data.
  - Clear-sky irradiance math and tilt geometry.
  - Reflected luminance conversion.
  - OWM client mocked responses (clear, overcast, radiation present).

- Integration tests:
  - End-to-end with recorded OWM fixtures and known expected ranges (noon clear, overcast, dusk).

CI/CD

- GitHub Actions
  - matrix: go: [1.20, 1.21, 1.22]
  - steps: golangci-lint, go test ./..., vet, build
  - release: build binaries for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64

Milestones
1. Spec finalised, repo init, SunPosition + OWM client contract + unit tests.
2. Implement ClearSky fallback, cloud attenuation, IlluminanceOnTilt, ReflectedLuminance, PerceivedLuminance.
3. Implement AnalyzeWithOWM, CLI wiring, tests using fixtures.
4. Performance tuning, caching, retries, error handling.
5. Optional: Perez sky model, specular reflection option, server endpoint.

Acceptance Criteria

- AnalyzeWithOWM returns Report for given lat/lon using OWM fixtures.
- Unit tests for geometric transforms and irradiance math pass.
- CLI `--lat/--lon` produce human-readable output and JSON/CSV formats.

Privacy & Rate Limits

- OWM API key required for external requests. Cache responses to limit calls. Document rate-limit recommendations for batch usage.

Deliverables

- PROJECT_SPEC.md (this file)
- brightness package with SunPosition, OWM client interface, ClearSky fallback, Illuminance/Reflection math, AnalyzeWithOWM.
- CLI `cmd/calcbright`.
- Unit and integration tests with OWM fixtures.
- GitHub Actions workflow.


OpenWeatherMap Current Weather API — quick tech summary

What it is
- Global current weather product providing temperature, cloudiness, visibility, sunrise/sunset times and other common meteorological variables.

Primary endpoints & auth
- Current Weather API: `https://api.openweathermap.org/data/2.5/weather?lat={lat}&lon={lon}&appid={key}&units=metric`.
- Auth: API key via `appid` param.

Key fields to use
- Current Weather response: `dt` (timestamp), `sunrise`, `sunset`, `clouds` (0..100), `visibility`, `weather` array, and `uvi` when available.

Behavioral / limits notes
- Handle 429 (rate limit) by surfacing error and increasing caching/TTL; avoid tight-loop retries.
- Retry policy: exponential backoff for network errors and 5xx, max 3 attempts. Do not retry on 4xx (401/403/429).
- HTTP timeout: recommend 5s per request.

Implementation tips for calcbright
- Call Current Weather API for the location. Extract cloud fraction and timing fields.
- Map: cloud fraction = `clouds.all / 100`.
- Use `uvi` (if available) to help tune luminous-efficacy heuristic.
- Cache Current Weather responses keyed by `lat,lon,rounded_time_window` (default round to 10 minutes). Default TTL 10m.
- Use `units=metric` for numeric consistency.

Example curl (Current Weather)

```bash
curl -s "https://api.openweathermap.org/data/2.5/weather?lat=47.6062&lon=-122.3321&units=metric&appid=YOUR_KEY"
```

Error mapping for code paths
- 401 → ErrMissingAPIKey / invalid key
- 429 → ErrRateLimited (recommend longer cache / lower call frequency)
- 5xx / network → transient; retry with backoff
- Missing fields → fallback to clear-sky + cloud attenuation

Bottom line for project
- Use Current Weather API as the single external source. Synthesize irradiance via clear-sky model and cloud attenuation. Cache responses (10m default). Handle 4xx/429 explicitly. Convert irradiance→lux via luminous-efficacy (default 120 lm/W) or derive from `uvi`.
