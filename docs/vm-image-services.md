# Stockyard VM Image: Systemd Services

This document explains which systemd services are enabled/disabled in the Stockyard VM image and why.

## Design Principles

1. **Ephemeral VMs**: Stockyard VMs are short-lived and disposable. Services for long-running systems (scheduled updates, periodic maintenance) are unnecessary.

2. **Fast Boot**: Every disabled service reduces boot time. We optimize for sub-3-second boots.

3. **Minimal Attack Surface**: Fewer services = fewer potential vulnerabilities.

4. **Stockyard-Managed**: VM initialization is handled by `stockyard-init.sh`, not cloud-init or other generic tools.

## Enabled Services

### Core System
| Service | Purpose |
|---------|---------|
| `systemd-journald` | System logging (required) |
| `systemd-networkd` | Network configuration |
| `systemd-udevd` | Device management |
| `dbus` | IPC (required by Tailscale, polkit) |

### Stockyard-Specific
| Service | Purpose |
|---------|---------|
| `stockyard-init.service` | VM initialization (hostname, SSH keys, Tailscale) |
| `stockyard-shell.service` | Vsock terminal access |
| `ssh.socket` | SSH access (socket-activated) |

### Network
| Service | Purpose |
|---------|---------|
| `systemd-networkd-wait-online` | Waits for network (configured for fast timeout) |
| `systemd-network-generator` | Generates network units from kernel cmdline |

## Disabled Services

### Timers (Scheduled Tasks)
| Timer | Reason Disabled |
|-------|-----------------|
| `apt-daily.timer` | VMs don't need scheduled apt updates |
| `apt-daily-upgrade.timer` | VMs don't need scheduled upgrades |
| `dpkg-db-backup.timer` | No need to backup dpkg database |
| `e2scrub_all.timer` | No periodic filesystem scrubbing needed |
| `motd-news.timer` | No MOTD news fetching needed |
| `fstrim.timer` | No periodic TRIM on ephemeral rootfs |
| `systemd-tmpfiles-clean.timer` | Short-lived VMs don't need temp cleanup |

### Services
| Service | Reason Disabled |
|---------|-----------------|
| `e2scrub_reap.service` | No ext4 scrubbing needed |
| `unattended-upgrades.service` | VMs don't auto-upgrade |
| `networkd-dispatcher.service` | No network event scripts needed |
| `systemd-pstore.service` | No persistent store for crash dumps |
| `systemd-timesyncd.service` | VM time syncs from host via KVM clock |
| `systemd-resolved` | Causes delays; DNS set by stockyard-init.sh |
| `getty@tty1.service` | Vsock shell provides console access |
| `console-getty.service` | Vsock shell provides console access |

### Masked Services (cloud-init)
| Service | Reason Masked |
|---------|---------------|
| `cloud-init.service` | stockyard-init.sh handles initialization |
| `cloud-init-local.service` | Not using cloud-init |
| `cloud-config.service` | Not using cloud-init |
| `cloud-final.service` | Not using cloud-init |

## Services Under Consideration

### Keep for Now (Investigate Later)
| Service | Notes |
|---------|-------|
| `polkit.service` | May be needed by Tailscale or dbus operations |
| `ldconfig.service` | Could pre-bake linker cache in image |
| `systemd-binfmt.service` | Binary format support - low overhead |
| `systemd-update-done.service` | First-boot marker - low overhead |

## Boot Time Impact

Measured boot times (kernel start to stockyard-init complete):

| Configuration | Boot Time |
|---------------|-----------|
| All services enabled | ~6.2s |
| Optimized (current) | ~1.5-2s (without Tailscale) |
| Optimized (current) | ~2-3s (with working Tailscale) |

## Making Changes

To disable additional services, add to `vm-image/Dockerfile` Section 7b:

```dockerfile
RUN systemctl disable <service-name> 2>/dev/null || true
```

To mask a service (prevents it from being started by dependencies):

```dockerfile
RUN systemctl mask <service-name> 2>/dev/null || true
```

After changes, rebuild and deploy:

```bash
cd vm-image
make deploy
```
