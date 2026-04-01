package config

// VfkitConfig holds configuration for the vfkit VM backend (macOS).
type VfkitConfig struct {
	VfkitBin   string `json:"vfkit_bin"`   // Path to vfkit binary (default: "vfkit")
	KernelPath string `json:"kernel_path"` // Path to arm64 vmlinux kernel
	RootfsPath string `json:"rootfs_path"` // Path to base rootfs image
}
