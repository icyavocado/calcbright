udev rule to grant non-root write access to backlight brightness

Place this file as /etc/udev/rules.d/99-backlight.rules (run as root).

Contents:

SUBSYSTEM=="backlight", ACTION=="add", KERNEL=="*", RUN+="/bin/chgrp video /sys/class/backlight/%k/brightness", RUN+="/bin/chmod g+w /sys/class/backlight/%k/brightness"

Explanation:
- This changes the group of the brightness file to `video` and grants group write access when the device is added.
- Add your user to the video group: `sudo usermod -aG video $USER` and re-login.

Apply changes immediately (after creating the file):

sudo udevadm control --reload
sudo udevadm trigger --subsystem-match=backlight

Verify:

ls -l /sys/class/backlight/*/brightness
brightnessctl set 50%

If your distribution uses a different group than `video`, replace `video` with the appropriate group.
