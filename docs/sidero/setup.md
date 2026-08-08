# Sidero setup and recovery

This repository builds the Wings binary that exposes the Sidero API used by the
panel. The panel and node must be upgraded together when the protocol changes.

## 1. Update the panel

Run the normal panel deployment, migrations, and frontend build. In the admin
panel, open **Admin → Sidero Add-ons**, click **Repair & Rescan**, then update
and enable these add-ons:

- Sidero Installers Core
- Sidero Files
- Minecraft Core
- Minecraft Mods, Plugins, Modpacks, and Worlds as needed

Run each add-on health check after enabling it.

## 2. Configure marketplace providers

Open **Sidero Installers Core → Settings** in the panel admin area.

- Modrinth needs no API key and should remain enabled.
- Paste the raw CurseForge API key into **CurseForge API key**. Do not include
  `Bearer`, `x-api-key:`, quotes, or surrounding whitespace.
- Save the setting, then run the Installers Core health check.

The key is stored encrypted and never sent to the browser. A configured key
reported as `invalid-api-key` means CurseForge returned HTTP 401/403; a
`provider-unavailable` result means the panel cannot reach CurseForge over
HTTPS/DNS. The provider health cache lasts five minutes; saving a new key uses
a new cache key, but an explicit health check is still recommended.

## 3. Build and install Sidero Wings

On the Wings host, from this repository:

```sh
make sidero-release-check
make sidero-build
sudo install -o root -g root -m 0755 build/wings_sidero_linux_amd64 /usr/local/bin/wings
```

For an ARM64 host, install `build/wings_sidero_linux_arm64` instead. If Wings
is running in Docker, build and deploy the local image instead:

```sh
docker build --build-arg VERSION=$(git rev-parse --short HEAD) -t siderocloud/wings:sidero .
```

Keep the existing `/etc/pterodactyl`, Docker socket, server-data, log, and
temporary-storage mounts. The example compose file in this repository is YAML
valid and includes the `/run/wings` mount used by installations that expose it.

## 4. Configure the node

Use the panel-generated node token and node ID:

```sh
sudo wings configure \
  --panel-url https://panel.example.com \
  --token '<node-token>' \
  --node '<node-id>' \
  --config-path /etc/pterodactyl/config.yml \
  --override
```

Ensure the resulting configuration contains this Sidero block. Values shown
are safe production defaults:

```yaml
sidero:
  enabled: true
  protocol_version: 1
  installers:
    enabled: true
  worlds:
    enabled: true
  archives:
    inspection_enabled: true
  remote_download:
    enabled: true
    allow_private_networks: false
```

Restart Wings and verify its service status and logs:

```sh
sudo systemctl restart wings
sudo systemctl --no-pager --full status wings
sudo journalctl -u wings -n 100 --no-pager
```

The panel should then show `world_operations` in the node capabilities and a
healthy Worlds component. World discovery reads the configured Minecraft
`level-name`; it does not require an API key.

## 5. Troubleshooting checklist

1. Confirm the panel can reach the node on its configured Wings port.
2. Confirm `sidero.enabled`, `sidero.worlds.enabled`, and
   `sidero.installers.enabled` are true in `/etc/pterodactyl/config.yml`.
3. Run `wings diagnostics` and inspect the generated report locally before
   uploading it anywhere.
4. In the panel, run the Sidero Files, Minecraft Core, Worlds, and Installers
   Core health checks.
5. If Worlds still reports unavailable, open Files and verify the configured
   world directory contains `level.dat`; then rescan Worlds.
