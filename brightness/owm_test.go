package brightness

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// fixture response copied from previous live call (minimal fields)
const fixture = `{"coord":{"lon":-122.3328,"lat":47.6061},"weather":[{"id":800,"main":"Clear","description":"clear sky","icon":"01d"}],"base":"stations","main":{"temp":282.87,"feels_like":281.27,"temp_min":280.77,"temp_max":284.55,"pressure":1010,"humidity":64,"sea_level":1010,"grnd_level":1000},"visibility":10000,"wind":{"speed":3.09,"deg":150},"clouds":{"all":0},"dt":1777217424,"sys":{"type":2,"id":2009669,"country":"US","sunrise":1777208413,"sunset":1777259621},"timezone":-25200,"id":5809844,"name":"Seattle","cod":200}`

func TestGetCurrent_ParsesAndCaches(t *testing.T) {
	// start a local test server that returns the fixture
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fixture))
	}))
	defer ts.Close()

	// override base URL via env
	os.Setenv("OPENWEATHERMAP_BASE_URL", ts.URL)
	defer os.Unsetenv("OPENWEATHERMAP_BASE_URL")

	client := NewOWMClient("fakekey", 1*time.Minute, 2*time.Second)

	ctx := context.Background()
	cur, err := client.GetCurrent(ctx, 47.6062, -122.3321)
	if err != nil {
		t.Fatalf("GetCurrent failed: %v", err)
	}
	if cur.Clouds != 0 {
		t.Fatalf("expected clouds=0, got %d", cur.Clouds)
	}
	if cur.Visibility != 10000 {
		t.Fatalf("expected visibility=10000, got %d", cur.Visibility)
	}
	if cur.Dt == 0 {
		t.Fatalf("expected dt nonzero")
	}

	// second call should hit cache and return immediately
	cur2, err := client.GetCurrent(ctx, 47.6062, -122.3321)
	if err != nil {
		t.Fatalf("GetCurrent 2 failed: %v", err)
	}
	if cur2.Dt != cur.Dt {
		t.Fatalf("expected cached dt same, got %d vs %d", cur2.Dt, cur.Dt)
	}
}

func TestAnalyze_CLI_Integ(t *testing.T) {
	// test that AnalyzeWithOWM runs end-to-end against the same mocked endpoint
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fixture))
	}))
	defer ts.Close()

	os.Setenv("OPENWEATHERMAP_BASE_URL", ts.URL)
	defer os.Unsetenv("OPENWEATHERMAP_BASE_URL")

	opts := AnalyzeOptions{
		Time:             time.Unix(1777217424, 0),
		Location:         Location{Lat: 47.6062, Lon: -122.3321, AltMeters: 0},
		Orientation:      Orientation{AzimuthDeg: 180, TiltDeg: 110},
		Device:           DeviceSpec{DisplayNits: 300, Reflectance: 0.05},
		Environment:      Environment{GroundAlbedo: 0.2},
		LuminousEfficacy: 120,
		OWMKey:           "fakekey",
		CacheTTL:         1 * time.Minute,
		OWMTimeout:       2 * time.Second,
	}

	_, err := AnalyzeWithOWM(context.Background(), 47.6062, -122.3321, opts)
	if err != nil {
		t.Fatalf("AnalyzeWithOWM failed: %v", err)
	}
}
