package brightness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"
)

var (
	ErrMissingAPIKey  = errors.New("missing or invalid OpenWeatherMap API key")
	ErrRateLimited    = errors.New("rate limited by OpenWeatherMap")
	ErrOWMUnavailable = errors.New("OpenWeatherMap unavailable")
)

type OWMClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	ttl        time.Duration
	retries    int

	mu    sync.Mutex
	cache map[string]cachedOWM
}

type cachedOWM struct {
	value  OWMCurrent
	expiry time.Time
}

// OWMCurrent contains subset of fields we need from the Current Weather response.
type OWMCurrent struct {
	Dt         int64
	Sunrise    int64
	Sunset     int64
	Clouds     int
	Uvi        float64
	Visibility int
}

// NewOWMClient creates a client. If ttl or timeout are zero, defaults are used.
func NewOWMClient(apiKey string, ttl time.Duration, timeout time.Duration) *OWMClient {
	if ttl == 0 {
		ttl = 10 * time.Minute
	}
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	baseURL := "https://api.openweathermap.org"
	if env := os.Getenv("OPENWEATHERMAP_BASE_URL"); env != "" {
		baseURL = env
	}
	return &OWMClient{
		apiKey:     apiKey,
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: timeout},
		ttl:        ttl,
		retries:    3,
		cache:      make(map[string]cachedOWM),
	}
}

func (c *OWMClient) cacheKey(lat, lon float64) string {
	// Round lat/lon to 4 decimals, time window by ttl
	window := int64(c.ttl.Seconds())
	now := time.Now().Unix()
	slot := now / window
	return fmt.Sprintf("%.4f:%.4f:%d", lat, lon, slot)
}

// GetCurrent fetches current weather from the Current Weather API, with caching and retries.
func (c *OWMClient) GetCurrent(ctx context.Context, lat, lon float64) (OWMCurrent, error) {
	if c.apiKey == "" {
		return OWMCurrent{}, ErrMissingAPIKey
	}

	key := c.cacheKey(lat, lon)
	c.mu.Lock()
	if ent, ok := c.cache[key]; ok {
		if time.Now().Before(ent.expiry) {
			val := ent.value
			c.mu.Unlock()
			return val, nil
		}
	}
	c.mu.Unlock()

	// Build URL for Current Weather API
	u, _ := url.Parse(c.baseURL + "/data/2.5/weather")
	q := u.Query()
	q.Set("lat", fmt.Sprintf("%f", lat))
	q.Set("lon", fmt.Sprintf("%f", lon))
	q.Set("units", "metric")
	q.Set("appid", c.apiKey)
	u.RawQuery = q.Encode()

	var lastErr error
	for attempt := 0; attempt <= c.retries; attempt++ {
		req, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			// backoff
			select {
			case <-ctx.Done():
				return OWMCurrent{}, ctx.Err()
			case <-time.After(time.Duration(1<<attempt) * time.Second):
			}
			continue
		}

		// read body
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			return OWMCurrent{}, ErrMissingAPIKey
		}
		if resp.StatusCode == 429 {
			return OWMCurrent{}, ErrRateLimited
		}
		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("owm server error: %d", resp.StatusCode)
			time.Sleep(time.Duration(1<<attempt) * time.Second)
			continue
		}
		if resp.StatusCode != 200 {
			return OWMCurrent{}, fmt.Errorf("owm unexpected status %d", resp.StatusCode)
		}

		// Parse JSON - Current Weather response (top-level fields)
		var parsed struct {
			Dt         int64 `json:"dt"`
			Visibility int   `json:"visibility"`
			Clouds     struct {
				All int `json:"all"`
			} `json:"clouds"`
			Sys struct {
				Sunrise int64 `json:"sunrise"`
				Sunset  int64 `json:"sunset"`
			} `json:"sys"`
			Uvi *float64 `json:"uvi"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			return OWMCurrent{}, err
		}

		var cur OWMCurrent
		cur.Dt = parsed.Dt
		cur.Visibility = parsed.Visibility
		cur.Clouds = parsed.Clouds.All
		cur.Sunrise = parsed.Sys.Sunrise
		cur.Sunset = parsed.Sys.Sunset
		if parsed.Uvi != nil {
			cur.Uvi = *parsed.Uvi
		}

		// cache result
		c.mu.Lock()
		c.cache[key] = cachedOWM{value: cur, expiry: time.Now().Add(c.ttl)}
		c.mu.Unlock()

		return cur, nil
	}

	if lastErr != nil {
		return OWMCurrent{}, lastErr
	}
	return OWMCurrent{}, ErrOWMUnavailable
}

// AnalyzeWithOWM fetches OWM data then runs analysis.
func AnalyzeWithOWM(ctx context.Context, lat, lon float64, opts AnalyzeOptions) (Report, error) {
	key := opts.OWMKey
	if key == "" {
		key = os.Getenv("OPENWEATHERMAP_API_KEY")
	}
	if key == "" {
		return Report{}, ErrMissingAPIKey
	}

	ttl := opts.CacheTTL
	timeout := opts.OWMTimeout
	client := NewOWMClient(key, ttl, timeout)
	// Respect user-provided cache TTL if AnalyzeOptions had CacheTTL field; keep default otherwise.
	ctx2 := ctx
	if ctx2 == nil {
		ctx2 = context.Background()
	}

	owm, err := client.GetCurrent(ctx2, lat, lon)
	if err != nil {
		return Report{}, err
	}

	// pick time: prefer explicit option, then API timestamp, then now
	t := opts.Time
	if t.IsZero() {
		if owm.Dt != 0 {
			t = time.Unix(owm.Dt, 0)
		} else {
			t = time.Now()
		}
	}

	// Synthesize irradiance via clear-sky model then attenuate by cloud fraction from OWM
	dni, dhi, ghi, err := ClearSkyIrradiance(t, Location{Lat: lat, Lon: lon, AltMeters: opts.Location.AltMeters})
	if err != nil {
		return Report{}, err
	}
	// Allow caller to override cloud fraction via opts.Environment; otherwise use OWM clouds
	const defaultCloud = 0.5
	cloudFrac := float64(owm.Clouds) / 100.0
	if opts.Environment.CloudFraction != 0 && opts.Environment.CloudFraction != defaultCloud {
		cloudFrac = opts.Environment.CloudFraction
	}
	dni, dhi, ghi = ApplyCloudAttenuation(dni, dhi, ghi, cloudFrac)

	luminous := opts.LuminousEfficacy
	if luminous <= 0 {
		luminous = 120
	}

	orient := opts.Orientation
	dev := opts.Device
	env := opts.Environment
	if env.GroundAlbedo == 0 {
		env.GroundAlbedo = 0.2
	}

	report, err := AnalyzeWithValues(t, Location{Lat: lat, Lon: lon, AltMeters: opts.Location.AltMeters}, orient, dev, env, luminous, dni, dhi, ghi)
	if err != nil {
		return Report{}, err
	}
	report.DataSource = "owm"
	return report, nil
}
