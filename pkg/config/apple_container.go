package config

// AppleContainerConfig holds configuration for the Apple `container` VM backend (macOS).
type AppleContainerConfig struct {
	ContainerBin string `json:"container_bin"` // Path to the `container` binary (default: "container")
	Image        string `json:"image"`         // OCI image reference for task containers
}
