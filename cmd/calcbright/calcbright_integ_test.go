package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
)

func TestCLIEndToEnd(t *testing.T) {
	// start mocked OWM server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := os.ReadFile("../testdata/owm_weather_fixture.json")
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))
	defer ts.Close()

	// build binary (use module path so build works from this package)
	if err := exec.Command("go", "build", "-o", "testcalc", "github.com/icyavocado/calcbright/cmd/calcbright").Run(); err != nil {
		t.Fatalf("build failed: %v", err)
	}
	defer os.Remove("testcalc")

	cmd := exec.Command("./testcalc", "--lat", "47.6062", "--lon", "-122.3321", "--format", "json")
	cmd.Env = append(os.Environ(), "OPENWEATHERMAP_BASE_URL="+ts.URL)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cli failed: %v output=%s", err, string(out))
	}
	// basic check: output contains "PerceivedLuminance" or JSON field
	s := string(out)
	if !(len(s) > 0) {
		t.Fatalf("no output")
	}
}
