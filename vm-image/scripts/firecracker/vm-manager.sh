#!/bin/bash
# Stockyard VM Manager - Direct Firecracker Management
# Usage: vm-manager.sh <command> [options]
#
# Commands:
#   start <vm-name>   Start a VM
#   stop <vm-name>    Stop a VM
#   status [vm-name]  Show VM status
#   list              List all VMs
#   clean <vm-name>   Clean up VM resources

set -e

# Configuration
VM_STATE_DIR="${VM_STATE_DIR:-/var/lib/stockyard/vms}"
FIRECRACKER_BIN="${FIRECRACKER_BIN:-/usr/local/bin/firecracker}"
ROOTFS_PATH="${ROOTFS_PATH:-/home/jesse/git/stockyard/vm-image/output/rootfs.ext4}"
KERNEL_PATH="${KERNEL_PATH:-/tmp/vmlinux.bin}"
BRIDGE_NAME="${BRIDGE_NAME:-flbr0}"
DEFAULT_VCPUS=2
DEFAULT_MEM_MB=2048

# Ensure running as root
check_root() {
    if [[ $EUID -ne 0 ]]; then
        echo "Error: This script must be run as root" >&2
        exit 1
    fi
}

# Create VM state directory
init_vm_dir() {
    local vm_name="$1"
    local vm_dir="${VM_STATE_DIR}/${vm_name}"
    mkdir -p "${vm_dir}"
    echo "${vm_dir}"
}

# Generate a random MAC address
generate_mac() {
    printf '02:%02x:%02x:%02x:%02x:%02x' $((RANDOM%256)) $((RANDOM%256)) $((RANDOM%256)) $((RANDOM%256)) $((RANDOM%256))
}

# Create tap device and attach to bridge
create_tap() {
    local tap_name="$1"

    # Create tap device
    ip tuntap add dev "${tap_name}" mode tap
    ip link set "${tap_name}" up

    # Attach to bridge if it exists
    if ip link show "${BRIDGE_NAME}" &>/dev/null; then
        ip link set "${tap_name}" master "${BRIDGE_NAME}"
    fi

    echo "Created tap device: ${tap_name}"
}

# Delete tap device
delete_tap() {
    local tap_name="$1"
    if ip link show "${tap_name}" &>/dev/null; then
        ip link delete "${tap_name}"
        echo "Deleted tap device: ${tap_name}"
    fi
}

# Generate Firecracker config (NO mmds-config!)
generate_config() {
    local vm_dir="$1"
    local vm_name="$2"
    local tap_name="$3"
    local mac_addr="$4"
    local vcpus="${5:-$DEFAULT_VCPUS}"
    local mem_mb="${6:-$DEFAULT_MEM_MB}"

    # Create a copy of the rootfs for this VM
    local vm_rootfs="${vm_dir}/rootfs.ext4"
    if [[ ! -f "${vm_rootfs}" ]]; then
        cp "${ROOTFS_PATH}" "${vm_rootfs}"
        echo "Created VM rootfs: ${vm_rootfs}"
    fi

    cat > "${vm_dir}/config.json" << EOF
{
  "boot-source": {
    "kernel_image_path": "${KERNEL_PATH}",
    "boot_args": "console=ttyS0 reboot=k panic=1 pci=off root=/dev/vda rw"
  },
  "drives": [
    {
      "drive_id": "rootfs",
      "path_on_host": "${vm_rootfs}",
      "is_root_device": true,
      "is_read_only": false
    }
  ],
  "machine-config": {
    "vcpu_count": ${vcpus},
    "mem_size_mib": ${mem_mb},
    "smt": false
  },
  "network-interfaces": [
    {
      "iface_id": "eth0",
      "guest_mac": "${mac_addr}",
      "host_dev_name": "${tap_name}"
    }
  ]
}
EOF
    echo "Generated config: ${vm_dir}/config.json"
}

# Start a VM
cmd_start() {
    local vm_name="$1"
    if [[ -z "${vm_name}" ]]; then
        echo "Usage: $0 start <vm-name>" >&2
        exit 1
    fi

    check_root

    local vm_dir=$(init_vm_dir "${vm_name}")
    local pid_file="${vm_dir}/firecracker.pid"

    # Check if already running
    if [[ -f "${pid_file}" ]] && kill -0 "$(cat ${pid_file})" 2>/dev/null; then
        echo "VM ${vm_name} is already running (PID: $(cat ${pid_file}))"
        exit 1
    fi

    # Create tap device
    local tap_name="tap-${vm_name:0:8}"
    delete_tap "${tap_name}" 2>/dev/null || true
    create_tap "${tap_name}"

    # Generate MAC and config
    local mac_addr=$(generate_mac)
    generate_config "${vm_dir}" "${vm_name}" "${tap_name}" "${mac_addr}"

    # Save tap name for cleanup
    echo "${tap_name}" > "${vm_dir}/tap_name"
    echo "${mac_addr}" > "${vm_dir}/mac_addr"

    # Start Firecracker
    echo "Starting VM ${vm_name}..."
    "${FIRECRACKER_BIN}" \
        --no-api \
        --config-file "${vm_dir}/config.json" \
        > "${vm_dir}/stdout.log" \
        2> "${vm_dir}/stderr.log" &

    local pid=$!
    echo "${pid}" > "${pid_file}"

    # Wait a moment and check if it started
    sleep 1
    if kill -0 "${pid}" 2>/dev/null; then
        echo "VM ${vm_name} started successfully (PID: ${pid})"
        echo "  Tap device: ${tap_name}"
        echo "  MAC address: ${mac_addr}"
        echo "  Console: screen ${vm_dir}/stdout.log (or check stderr.log for errors)"
    else
        echo "VM ${vm_name} failed to start. Check ${vm_dir}/stderr.log"
        cat "${vm_dir}/stderr.log"
        delete_tap "${tap_name}"
        exit 1
    fi
}

# Stop a VM
cmd_stop() {
    local vm_name="$1"
    if [[ -z "${vm_name}" ]]; then
        echo "Usage: $0 stop <vm-name>" >&2
        exit 1
    fi

    check_root

    local vm_dir="${VM_STATE_DIR}/${vm_name}"
    local pid_file="${vm_dir}/firecracker.pid"

    if [[ ! -f "${pid_file}" ]]; then
        echo "VM ${vm_name} is not running"
        exit 1
    fi

    local pid=$(cat "${pid_file}")
    if kill -0 "${pid}" 2>/dev/null; then
        echo "Stopping VM ${vm_name} (PID: ${pid})..."
        kill "${pid}"
        sleep 1
        if kill -0 "${pid}" 2>/dev/null; then
            kill -9 "${pid}"
        fi
    fi

    rm -f "${pid_file}"

    # Clean up tap device
    if [[ -f "${vm_dir}/tap_name" ]]; then
        delete_tap "$(cat ${vm_dir}/tap_name)"
    fi

    echo "VM ${vm_name} stopped"
}

# Show VM status
cmd_status() {
    local vm_name="$1"

    if [[ -n "${vm_name}" ]]; then
        local vm_dir="${VM_STATE_DIR}/${vm_name}"
        local pid_file="${vm_dir}/firecracker.pid"

        if [[ -f "${pid_file}" ]] && kill -0 "$(cat ${pid_file})" 2>/dev/null; then
            echo "VM ${vm_name}: running (PID: $(cat ${pid_file}))"
            [[ -f "${vm_dir}/mac_addr" ]] && echo "  MAC: $(cat ${vm_dir}/mac_addr)"
            [[ -f "${vm_dir}/tap_name" ]] && echo "  Tap: $(cat ${vm_dir}/tap_name)"
        else
            echo "VM ${vm_name}: stopped"
        fi
    else
        cmd_list
    fi
}

# List all VMs
cmd_list() {
    if [[ ! -d "${VM_STATE_DIR}" ]]; then
        echo "No VMs found"
        return
    fi

    echo "VMs in ${VM_STATE_DIR}:"
    for vm_dir in "${VM_STATE_DIR}"/*; do
        if [[ -d "${vm_dir}" ]]; then
            local vm_name=$(basename "${vm_dir}")
            local pid_file="${vm_dir}/firecracker.pid"
            if [[ -f "${pid_file}" ]] && kill -0 "$(cat ${pid_file})" 2>/dev/null; then
                echo "  ${vm_name}: running (PID: $(cat ${pid_file}))"
            else
                echo "  ${vm_name}: stopped"
            fi
        fi
    done
}

# Clean up VM resources
cmd_clean() {
    local vm_name="$1"
    if [[ -z "${vm_name}" ]]; then
        echo "Usage: $0 clean <vm-name>" >&2
        exit 1
    fi

    check_root

    # Stop first if running
    cmd_stop "${vm_name}" 2>/dev/null || true

    local vm_dir="${VM_STATE_DIR}/${vm_name}"
    if [[ -d "${vm_dir}" ]]; then
        rm -rf "${vm_dir}"
        echo "Cleaned up VM ${vm_name}"
    else
        echo "VM ${vm_name} not found"
    fi
}

# Main
case "${1:-}" in
    start)
        cmd_start "$2"
        ;;
    stop)
        cmd_stop "$2"
        ;;
    status)
        cmd_status "$2"
        ;;
    list)
        cmd_list
        ;;
    clean)
        cmd_clean "$2"
        ;;
    *)
        echo "Stockyard VM Manager"
        echo ""
        echo "Usage: $0 <command> [options]"
        echo ""
        echo "Commands:"
        echo "  start <vm-name>   Start a VM"
        echo "  stop <vm-name>    Stop a VM"
        echo "  status [vm-name]  Show VM status"
        echo "  list              List all VMs"
        echo "  clean <vm-name>   Clean up VM resources"
        echo ""
        echo "Environment variables:"
        echo "  VM_STATE_DIR      VM state directory (default: /var/lib/stockyard/vms)"
        echo "  FIRECRACKER_BIN   Firecracker binary (default: /usr/local/bin/firecracker)"
        echo "  ROOTFS_PATH       Path to rootfs.ext4"
        echo "  KERNEL_PATH       Path to kernel"
        echo "  BRIDGE_NAME       Network bridge name (default: flbr0)"
        exit 1
        ;;
esac
