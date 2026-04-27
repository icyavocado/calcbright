Example systemd unit for running calcbright as a user service.

Place this file at ~/.config/systemd/user/calcbright.service and adjust ExecStart flags.

[Unit]
Description=Ambient-aware Display Brightness (calcbright)
After=network.target

[Service]
Type=simple
Environment="OPENWEATHERMAP_API_KEY=your_api_key_here"
ExecStart=/usr/local/bin/calcbright --lat 47.6062 --lon -122.3321 --daemon --interval 30s --owm-api-key $OPENWEATHERMAP_API_KEY --display-nits 300 --brightnessctl-device ""
Restart=on-failure
KillMode=process

[Install]
WantedBy=default.target

Notes:
- Use `systemctl --user enable --now calcbright.service` to start and enable on login.
- For system-wide service (requires root), drop the file into /etc/systemd/system/ and remove `--user` from systemctl.
- Prefer using a dedicated install path for the binary (e.g. /usr/local/bin) and configuring `OPENWEATHERMAP_API_KEY` via systemd EnvironmentFile or a secure secret mechanism.
