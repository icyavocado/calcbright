calcbright
=========

Adaptive display brightness based on solar irradiance and optional OpenWeatherMap data.

Overview
- calcbright computes perceived display luminance and recommended display brightness using a clear-sky model and optional OpenWeatherMap (OWM) observations.
- It can run once and print results, apply brightness via brightnessctl, or run as a daemon to adjust periodically.

Installation
- Build and install the command (recommended):

  ```sh
  go install github.com/icyavocado/calcbright/cmd/calcbright@latest
  ```

- Or build from source in the repository:

  ```sh
  git clone https://github.com/icyavocado/calcbright.git
  cd calcbright
  go build ./cmd/calcbright
  ```

Quick start / Configuration
- The program accepts flags for location, device, and behavior. You must provide latitude/longitude.
- Supply an OpenWeatherMap API key via environment variable OPENWEATHERMAP_API_KEY when you want OWM data.

- Example one-liners (replace placeholders):

  ```sh
  # Run once and print text output
  OPENWEATHERMAP_API_KEY=YOUR_KEY calcbright --lat 47.6062 --lon -122.3321

  # Run once and apply recommended brightness (requires brightnessctl and permissions)
  OPENWEATHERMAP_API_KEY=YOUR_KEY calcbright --lat 47.6062 --lon -122.3321 --apply --display-nits 300 --sudo

  # Run as a daemon, check every 30s and apply when needed
  OPENWEATHERMAP_API_KEY=YOUR_KEY calcbright --lat 47.6062 --lon -122.3321 --daemon --interval 30s --apply
  ```

Common flags
- `--lat`, `--lon`         Latitude and longitude (required)
- `--display-nits`       Device maximum nits (used to map recommended nits to percent)
- `--apply`              Apply brightness using brightnessctl
- `--daemon`             Run continuously and adjust periodically
- `--owm-api-key`        Provide OWM key via flag instead of OPENWEATHERMAP_API_KEY env
- `--format`             Output format: `text`|`json`|`csv`

Usage examples
- Example output (text):

  ```text
  Perceived nits: 512.34, Reflected: 21.12, Contrast: 14.23, Glare: false
  ```

Running as a systemd service
- Example unit (install to `/etc/systemd/system/calcbright.service`):

  ```ini
  [Unit]
  Description=calcbright adaptive backlight
  After=network.target

  [Service]
  Type=simple
  Environment=OPENWEATHERMAP_API_KEY=YOUR_KEY
  ExecStart=/usr/local/bin/calcbright --lat 47.6062 --lon -122.3321 --daemon --apply
  Restart=on-failure

  [Install]
  WantedBy=multi-user.target
  ```

udev example
- If you prefer not to run as root, grant write access to backlight device files via a udev rule (example):

  ```sh
  # /etc/udev/rules.d/99-backlight.rules
  SUBSYSTEM=="backlight", ACTION=="add", RUN+="/bin/chmod g+w /sys/class/backlight/%k/brightness"
  ```

Testing
- Run the unit tests locally:

  ```sh
  go test ./...
  ```

Contributing
- Issues and pull requests welcome. When contributing:
  - Open an issue first for non-trivial changes.
  - Keep changes small and focused.
  - Add or update tests for behavioral changes.

Code of Conduct
- Be respectful. Treat maintainers, contributors and users with professionalism. Report issues politely.

Maintainer
- Icy Avocado <avocado@ovacoda.com>

License
- This project is provided under the MIT License.
