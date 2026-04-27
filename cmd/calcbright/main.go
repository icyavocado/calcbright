package main

import (
    "context"
    "encoding/csv"
    "encoding/json"
    "flag"
    "fmt"
    "math"
    "os"
    "os/exec"
    "os/signal"
    "strings"
    "syscall"
    "time"

    "github.com/yourorg/calculate-brightness/brightness"
)

// shellJoin joins args for display in dry-run. It does not attempt to escape fully; for human-readable output only.
func shellJoin(args []string) string {
	var parts []string
	for _, a := range args {
		parts = append(parts, quoteShellArg(a))
	}
	return strings.Join(parts, " ")
}

// quoteShellArg returns a POSIX-shell-safe quoted form of s for display.
func quoteShellArg(s string) string {
	if s == "" {
		return "''"
	}
	need := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\'' || r == '"' || r == '$' || r == '&' || r == '|' || r == ';' || r == '<' || r == '>' || r == '*' || r == '?' || r == '(' || r == ')' || r == '[' || r == ']' || r == '{' || r == '}' || r == '\\' || r == '`' || r == '~' {
			need = true
			break
		}
	}
	if !need {
		return s
	}
	// single-quote and escape single quotes
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// runBrightness is the function used to actually apply brightness changes.
// It is a variable so tests can override it.
var runBrightness = func(pctInt int, device string, useSudo bool, dryRun bool) error {
    args := []string{}
    if device != "" {
        args = append(args, "-d", device)
    }
    args = append(args, "set", fmt.Sprintf("%d%%", pctInt))

    if dryRun {
        cmd := "brightnessctl " + shellJoin(args)
        if useSudo {
            cmd = "sudo " + cmd
        }
        fmt.Fprintln(os.Stderr, "DRY RUN:", cmd)
        return nil
    }

    // find brightnessctl
    path, err := exec.LookPath("brightnessctl")
    if err != nil {
        return fmt.Errorf("brightnessctl not found in PATH; cannot apply brightness")
    }
    var cmd *exec.Cmd
    if useSudo {
        sudoPath, err := exec.LookPath("sudo")
        if err != nil {
            return fmt.Errorf("sudo not found in PATH; cannot run with sudo")
        }
        cmd = exec.Command(sudoPath, append([]string{path}, args...)...)
    } else {
        cmd = exec.Command(path, args...)
    }
    cmd.Stdout = os.Stderr
    cmd.Stderr = os.Stderr
    if err := cmd.Run(); err != nil {
        return err
    }
    return nil
}

// maskKey masks API key for logging.
func maskKey(k string) string {
    if k == "" {
        return "<none>"
    }
    if len(k) <= 8 {
        return k
    }
    return k[:4] + "..." + k[len(k)-4:]
}

// computeReport encapsulates analysis logic so it can be called repeatedly by daemon mode
func computeReport(ctx context.Context, opts brightness.AnalyzeOptions, lat, lon float64, useOWM bool, envKey string, debug bool) (brightness.Report, error) {
    var r brightness.Report
    key := opts.OWMKey
    if key == "" {
        key = envKey
    }

    if useOWM {
        if debug {
            src := "env"
            if opts.OWMKey != "" {
                src = "flag"
            }
            fmt.Fprintln(os.Stderr, "OWM key source:", src)
            fmt.Fprintln(os.Stderr, "OWM key:", maskKey(key))

            client := brightness.NewOWMClient(key, opts.CacheTTL, opts.OWMTimeout)
            owmCur, gerr := client.GetCurrent(ctx, lat, lon)
            if gerr != nil {
                // fall back to local model
                return brightness.AnalyzeWithValues(opts.Time, opts.Location, opts.Orientation, opts.Device, opts.Environment, opts.LuminousEfficacy, 0, 0, 0)
            }

            enc := json.NewEncoder(os.Stderr)
            enc.SetIndent("", "  ")
            enc.Encode(owmCur)

            dni, dhi, ghi, err := brightness.ClearSkyIrradiance(opts.Time, opts.Location)
            if err != nil {
                return r, err
            }
            cloudFrac := float64(owmCur.Clouds) / 100.0
            dni, dhi, ghi = brightness.ApplyCloudAttenuation(dni, dhi, ghi, cloudFrac)

            r, err = brightness.AnalyzeWithValues(opts.Time, opts.Location, opts.Orientation, opts.Device, opts.Environment, opts.LuminousEfficacy, dni, dhi, ghi)
            if err != nil {
                return r, err
            }
            r.DataSource = "owm"
            return r, nil
        }

        // non-debug OWM path
        r2, err := brightness.AnalyzeWithOWM(ctx, lat, lon, opts)
        if err != nil {
            // fallback to clear-sky model
            return brightness.AnalyzeWithValues(opts.Time, opts.Location, opts.Orientation, opts.Device, opts.Environment, opts.LuminousEfficacy, 0, 0, 0)
        }
        return r2, nil
    }

    // no OWM: local model
    return brightness.AnalyzeWithValues(opts.Time, opts.Location, opts.Orientation, opts.Device, opts.Environment, opts.LuminousEfficacy, 0, 0, 0)
}

// daemonLoop runs the periodic daemon logic. It returns when a signal is received on stop.
func daemonLoop(initialReport brightness.Report, compute func() (brightness.Report, error), display float64, interval time.Duration, smoothingAlpha float64, hysteresisPercent float64, minApplyInterval time.Duration, brightnessctlDevice string, useSudo bool, dryRun bool, stop <-chan os.Signal) {
    lastApplied := 0
    lastAppliedTime := time.Now().Add(-minApplyInterval - time.Second)
    var smoothed float64 = float64(initialReport.RecommendedDisplayNits)
    ticker := time.NewTicker(interval)
    defer ticker.Stop()
    for {
        select {
        case <-stop:
            fmt.Fprintln(os.Stderr, "stopping daemon")
            return
        case <-ticker.C:
            r, err := compute()
            if err != nil {
                fmt.Fprintln(os.Stderr, "compute error:", err)
                continue
            }
            // apply exponential smoothing
            smoothed = (smoothingAlpha)*r.RecommendedDisplayNits + (1-smoothingAlpha)*smoothed
            // compute percent
            pct := 100.0 * smoothed / display
            if pct < 1.0 {
                pct = 1.0
            }
            if pct > 100.0 {
                pct = 100.0
            }
            pctInt := int(math.Round(pct))
            // hysteresis: only apply if absolute percent change >= hysteresis
            if math.Abs(float64(pctInt-lastApplied)) >= hysteresisPercent {
                // respect minimum apply interval
                if time.Since(lastAppliedTime) >= minApplyInterval {
                    if err := runBrightness(pctInt, brightnessctlDevice, useSudo, dryRun); err != nil {
                        fmt.Fprintln(os.Stderr, "apply error:", err)
                    } else {
                        fmt.Fprintln(os.Stderr, "applied brightness (daemon):", pctInt, "%")
                        lastApplied = pctInt
                        lastAppliedTime = time.Now()
                    }
                }
            }
        }
    }
}

func main() {
	fs := flag.NewFlagSet("calcbright", flag.ExitOnError)
	lat := fs.Float64("lat", 9999, "latitude (required)")
	lon := fs.Float64("lon", 9999, "longitude (required)")
	alt := fs.Float64("alt", 0, "altitude meters")
	az := fs.Float64("az", 180, "screen azimuth deg")
	tilt := fs.Float64("tilt", 110, "screen tilt deg")
	cloud := fs.Float64("cloud", 0.5, "cloud fraction 0..1")
	display := fs.Float64("display-nits", 300, "display nits")
	reflect := fs.Float64("reflectance", 0.05, "screen reflectance 0..1")
	format := fs.String("format", "text", "output format: text|json|csv")
	owmKey := fs.String("owm-api-key", "", "OpenWeatherMap API key")
	debug := fs.Bool("debug", false, "print debug info about OWM and inputs")
	owmCacheTTL := fs.Duration("owm-cache-ttl", 10*time.Minute, "cache TTL for OpenWeatherMap responses")
	owmTimeout := fs.Duration("owm-timeout", 5*time.Second, "HTTP timeout for OpenWeatherMap requests")
	apply := fs.Bool("apply", false, "apply recommended brightness using brightnessctl")
	brightnessctlDevice := fs.String("brightnessctl-device", "", "device name passed to brightnessctl via -d")
	dryRun := fs.Bool("dry-run", false, "show the brightnessctl command without executing it")
	useSudo := fs.Bool("sudo", false, "run brightnessctl via sudo if necessary")
	timeStr := fs.String("time", "now", "RFC3339 time or 'now'")
	// daemon mode and smoothing/hysteresis
	daemon := fs.Bool("daemon", false, "run continuously and adjust brightness periodically")
	interval := fs.Duration("interval", 30*time.Second, "interval between checks when in daemon mode")
	hysteresis := fs.Float64("hysteresis-percent", 3.0, "minimum absolute percent change before applying a new brightness")
	smoothingAlpha := fs.Float64("smoothing-alpha", 0.3, "exponential smoothing alpha 0..1 (0=no smoothing)")
	minApplyInterval := fs.Duration("min-apply-interval", 30*time.Second, "minimum time between applying brightness changes")

	fs.Parse(os.Args[1:])

	if *lat == 9999 || *lon == 9999 {
		fmt.Println("usage: calcbright --lat <lat> --lon <lon> [flags]")
		fs.PrintDefaults()
		os.Exit(2)
	}

	var t time.Time
	var err error
	if *timeStr == "now" || *timeStr == "" {
		t = time.Now()
	} else {
		t, err = time.Parse(time.RFC3339, *timeStr)
		if err != nil {
			fmt.Fprintln(os.Stderr, "invalid time:", err)
			os.Exit(1)
		}
	}

	opts := brightness.AnalyzeOptions{
		Time:             t,
		Location:         brightness.Location{Lat: *lat, Lon: *lon, AltMeters: *alt},
		Orientation:      brightness.Orientation{AzimuthDeg: *az, TiltDeg: *tilt},
		Device:           brightness.DeviceSpec{DisplayNits: *display, Reflectance: *reflect},
		Environment:      brightness.Environment{CloudFraction: *cloud, GroundAlbedo: 0.2},
		LuminousEfficacy: 120,
		CacheTTL:         *owmCacheTTL,
		OWMTimeout:       *owmTimeout,
	}

	// Determine whether to use OWM
	envKey := os.Getenv("OPENWEATHERMAP_API_KEY")
	useOWM := (*owmKey != "") || (envKey != "")
	if *owmKey != "" {
		opts.OWMKey = *owmKey
	}

    // computeReport is wrapped above to make daemon testing easier

    // compute first report synchronously
    var report brightness.Report
    report, err = computeReport(context.Background(), opts, *lat, *lon, useOWM, envKey, *debug)
    if err != nil {
        fmt.Fprintln(os.Stderr, "error:", err)
        os.Exit(1)
    }

	switch *format {
	case "text":
		fmt.Printf("Perceived nits: %.2f, Reflected: %.2f, Contrast: %.2f, Glare: %v\n",
			report.PerceivedLuminance, report.ReflectedLuminance, report.ContrastRatio, report.Glare)
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintln(os.Stderr, "json encode error:", err)
			os.Exit(1)
		}
	case "csv":
		w := csv.NewWriter(os.Stdout)
		row := []string{"time", "lat", "lon", "perceived_nits", "reflected", "contrast", "glare"}
		w.Write(row)
		row2 := []string{report.Time.Format(time.RFC3339), fmt.Sprintf("%.6f", report.Location.Lat), fmt.Sprintf("%.6f", report.Location.Lon), fmt.Sprintf("%.3f", report.PerceivedLuminance), fmt.Sprintf("%.3f", report.ReflectedLuminance), fmt.Sprintf("%.3f", report.ContrastRatio), fmt.Sprintf("%v", report.Glare)}
		w.Write(row2)
		w.Flush()
	default:
		fmt.Println("unsupported format")
	}

    // Optionally apply recommended brightness via brightnessctl
    if *apply {
		// Recommended nits are in report.RecommendedDisplayNits. Map to percentage of device max (display-nits flag).
		recommended := report.RecommendedDisplayNits
		deviceMax := *display
		if deviceMax <= 0 {
			deviceMax = recommended
		}
		pct := 100.0 * recommended / deviceMax
		if math.IsNaN(pct) || math.IsInf(pct, 0) {
			fmt.Fprintln(os.Stderr, "cannot compute brightness percentage")
			os.Exit(1)
		}
		if pct < 1.0 {
			pct = 1.0
		}
		if pct > 100.0 {
			pct = 100.0
		}
		pctInt := int(math.Round(pct))

		// Build brightnessctl command pieces
		args := []string{}
		if *brightnessctlDevice != "" {
			args = append(args, "-d", *brightnessctlDevice)
		}
		args = append(args, "set", fmt.Sprintf("%d%%", pctInt))

        if err := runBrightness(pctInt, *brightnessctlDevice, *useSudo, *dryRun); err != nil {
            fmt.Fprintln(os.Stderr, "Can't modify brightness:", err)
            fmt.Fprintln(os.Stderr, "You should run this program with root privileges or grant write permissions to device files. See docs/udev-backlight.md for udev rule example.")
            os.Exit(1)
        }
        fmt.Fprintln(os.Stderr, "applied brightness:", pctInt, "%")
    }

	// If daemon mode requested, enter loop
    if *daemon {
        stop := make(chan os.Signal, 1)
        signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

        compute := func() (brightness.Report, error) {
            return computeReport(context.Background(), opts, *lat, *lon, useOWM, envKey, *debug)
        }
        daemonLoop(report, compute, *display, *interval, *smoothingAlpha, *hysteresis, *minApplyInterval, *brightnessctlDevice, *useSudo, *dryRun, stop)
    }
}
